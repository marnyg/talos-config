// Package docker renders leases as containers on one Docker Engine
// (protocol ADR-0009, task 0bc.4.4): run by image digest, the intro
// as an env var, the lease and owner as labels.
//
// Docker has no native deadline. Extend is bookkeeping (the
// provisioner's Sweep is the deadline while it lives), Kill is
// `rm -f`, and a restarted provisioner re-adopts by label with a
// grace deadline (ADR-0009 amendment) — so a container nobody
// extends is swept within one grace. The real mechanism is the
// child's self-lapse; AutoRemove erases a container whose child
// exited, so List never reports it.
//
// Verified on Docker Engine API 1.54 (2026-09-25): create with an
// image the daemon lacks is 404 "No such image: …" (classic store)
// or "no such image: …: image not known" (containerd store); POST
// images/create?fromImage=<name>&tag=sha256:<hex> pulls by digest and
// streams JSON lines with failures inline as {"error": ...} under a
// 200; containers/json takes filters={"label":[...],"status":[...]}
// and reports the digest reference the container was created with;
// rm?force=true is 204, 404 once gone.
//
// The API client is net/http over the daemon socket — five calls, no
// docker SDK. The image must be public or already present: registry
// auth is not v0.
package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/marnyg/talos-config/actors/driver"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/provisioner"
)

// APIVersion is the Engine API version every path is pinned to
// (1.40 is the oldest the current daemon serves; nothing here needs
// newer).
const APIVersion = "v1.40"

// Config is one daemon.
type Config struct {
	// Host is where the daemon listens: unix:///path/to/docker.sock or
	// tcp://host:port (plain HTTP). Empty ⇒ DOCKER_HOST, then
	// /var/run/docker.sock.
	Host string
}

// Driver implements provisioner.Driver on one daemon.
type Driver struct {
	http *http.Client
	base string
}

var _ provisioner.Driver = (*Driver)(nil)

// New builds the driver.
func New(cfg Config) (*Driver, error) {
	host := cfg.Host
	if host == "" {
		host = os.Getenv("DOCKER_HOST")
	}
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	u, err := url.Parse(host)
	if err != nil {
		return nil, fmt.Errorf("docker: host: %w", err)
	}
	tr := &http.Transport{}
	base := ""
	switch u.Scheme {
	case "unix":
		path := u.Path
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		}
		base = "http://docker"
	case "tcp", "http":
		base = "http://" + u.Host
	default:
		return nil, fmt.Errorf("docker: host scheme %q (want unix:// or tcp://)", u.Scheme)
	}
	return &Driver{http: &http.Client{Transport: tr}, base: base + "/" + APIVersion}, nil
}

// containerName is the Handle: the lease id under a fixed prefix.
func containerName(lease string) string { return "sap-" + lease }

// apiError is a non-2xx from the daemon, with its message.
type apiError struct {
	Code int
	Msg  string
}

func (e *apiError) Error() string { return fmt.Sprintf("docker: %d %s", e.Code, e.Msg) }

func (d *Driver) do(ctx context.Context, method, path string, body any, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, d.base+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return err
	}
	if res.StatusCode/100 != 2 {
		var st struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &st)
		if st.Message == "" {
			st.Message = strings.TrimSpace(string(data))
		}
		return &apiError{Code: res.StatusCode, Msg: st.Message}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

type createBody struct {
	Image      string            `json:"Image"`
	Env        []string          `json:"Env"`
	Labels     map[string]string `json:"Labels"`
	HostConfig struct {
		AutoRemove bool `json:"AutoRemove"`
	} `json:"HostConfig"`
}

// Start creates and starts the container, pulling the image by digest
// first if the daemon lacks it. The deadline in spec is ignored: this
// platform has none.
func (d *Driver) Start(ctx context.Context, spec provisioner.StartSpec) (provisioner.Handle, error) {
	if err := provisioner.CheckImage(spec.Image); err != nil {
		return "", err
	}
	name := containerName(spec.Lease)
	body := createBody{
		Image:  spec.Image,
		Env:    []string{driver.ParamsEnv + "=" + string(spec.Params)},
		Labels: map[string]string{provisioner.LabelLease: spec.Lease, provisioner.LabelOwner: string(spec.Owner)},
	}
	body.HostConfig.AutoRemove = true
	create := func() error {
		return d.do(ctx, http.MethodPost, "/containers/create?name="+url.QueryEscape(name), body, nil)
	}
	err := create()
	var ae *apiError
	if errors.As(err, &ae) && ae.Code == http.StatusNotFound && strings.Contains(strings.ToLower(ae.Msg), "no such image") {
		if err = d.pull(ctx, spec.Image); err != nil {
			return "", err
		}
		err = create()
	}
	if err != nil {
		return "", err
	}
	if err := d.do(ctx, http.MethodPost, "/containers/"+name+"/start", nil, nil); err != nil {
		// Created but not running: do not leave it for List to adopt.
		_ = d.Kill(context.WithoutCancel(ctx), provisioner.Handle(name))
		return "", err
	}
	return provisioner.Handle(name), nil
}

// pull fetches image (name@sha256:hex) and reads the progress stream
// to its end, failing on an inline error.
func (d *Driver) pull(ctx context.Context, image string) error {
	name, digest, _ := strings.Cut(image, "@")
	q := url.Values{"fromImage": {name}, "tag": {digest}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.base+"/images/create?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	res, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		data, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		return &apiError{Code: res.StatusCode, Msg: "pull: " + strings.TrimSpace(string(data))}
	}
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var line struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &line) == nil && line.Error != "" {
			return fmt.Errorf("docker: pull: %s", line.Error)
		}
	}
	return sc.Err()
}

// Extend is bookkeeping here: no native deadline. The provisioner's
// Sweep enforces it.
func (d *Driver) Extend(context.Context, provisioner.Handle, int64) error { return nil }

// Kill is rm -f. A container already gone is a successful kill.
func (d *Driver) Kill(ctx context.Context, h provisioner.Handle) error {
	err := d.do(ctx, http.MethodDelete, "/containers/"+string(h)+"?force=true", nil, nil)
	var ae *apiError
	if errors.As(err, &ae) && ae.Code == http.StatusNotFound {
		return nil
	}
	return err
}

// List returns every running container carrying the lease label. The
// image comes back as the daemon names it (a containerd-store daemon
// normalises: docker.io/library/…@sha256:…); the digest is stable.
func (d *Driver) List(ctx context.Context) ([]provisioner.Running, error) {
	filters, _ := json.Marshal(map[string][]string{"label": {provisioner.LabelLease}, "status": {"running"}})
	var items []struct {
		Names  []string          `json:"Names"`
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
	}
	if err := d.do(ctx, http.MethodGet, "/containers/json?filters="+url.QueryEscape(string(filters)), nil, &items); err != nil {
		return nil, err
	}
	var out []provisioner.Running
	for _, c := range items {
		r := provisioner.Running{
			Lease: c.Labels[provisioner.LabelLease],
			Owner: cert.ActorID(c.Labels[provisioner.LabelOwner]),
			Image: c.Image,
		}
		if len(c.Names) > 0 {
			r.Handle = provisioner.Handle(strings.TrimPrefix(c.Names[0], "/"))
		}
		out = append(out, r)
	}
	return out, nil
}
