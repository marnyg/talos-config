// Package k8s renders leases as Kubernetes Jobs (protocol ADR-0009,
// task 0bc.4.3): one Job per lease, by image digest, the intro as an
// env var, `activeDeadlineSeconds` as the deadline.
//
// Verified on the live cluster (2026-09-25, k8s 1.32): a Job's
// `activeDeadlineSeconds` is mutable both ways on a live Job and a
// shortened one fires — so Extend is native here, and Sweep is
// bookkeeping. The deadline is relative to `status.startTime`, so
// Extend reads the Job first.
//
// Labels: an actor id (`ed:` + 64 hex) is neither a valid label value
// (≤ 63 chars, no ':') nor short enough, so provisioner.LabelLease is
// a label (List selects on it) and provisioner.LabelOwner is an
// annotation. A Job already Failed or Complete is not Running and is
// not reported; ttlSecondsAfterFinished lets the platform GC it.
//
// The pod satisfies PodSecurity `restricted`: the child image must
// run as non-root (the recipe in 0bc.4.5 does).
//
// The API client is net/http against the Jobs endpoints — four calls,
// no client-go. InCluster reads the service-account mount.
package k8s

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/provisioner"
)

// ParamsEnv is the env var the child reads its intro from.
const ParamsEnv = "SAP_INTRO"

// Config is one API server and namespace.
type Config struct {
	Host      string // e.g. https://10.96.0.1:443
	Token     string // bearer; empty ⇒ no Authorization header
	CA        []byte // PEM; nil ⇒ system roots
	Namespace string
	// TTLAfterFinished is ttlSecondsAfterFinished on every Job; 0 ⇒
	// DefaultTTLAfterFinished.
	TTLAfterFinished int64
	// Now is the clock the deadline is computed against at Start;
	// nil ⇒ time.Now. Set it to the provisioner actor's clock.
	Now func() int64
}

// DefaultTTLAfterFinished keeps a finished Job around long enough to
// read why it ended, then lets the platform GC it.
const DefaultTTLAfterFinished int64 = 600

const saMount = "/var/run/secrets/kubernetes.io/serviceaccount"

// InCluster reads the pod's service-account mount and
// KUBERNETES_SERVICE_{HOST,PORT}.
func InCluster() (Config, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return Config{}, errors.New("k8s: not in a cluster (KUBERNETES_SERVICE_HOST/PORT unset)")
	}
	token, err := os.ReadFile(saMount + "/token")
	if err != nil {
		return Config{}, fmt.Errorf("k8s: %w", err)
	}
	ca, err := os.ReadFile(saMount + "/ca.crt")
	if err != nil {
		return Config{}, fmt.Errorf("k8s: %w", err)
	}
	ns, err := os.ReadFile(saMount + "/namespace")
	if err != nil {
		return Config{}, fmt.Errorf("k8s: %w", err)
	}
	return Config{Host: "https://" + host + ":" + port, Token: strings.TrimSpace(string(token)), CA: ca, Namespace: strings.TrimSpace(string(ns))}, nil
}

// Driver implements provisioner.Driver on one namespace.
type Driver struct {
	cfg  Config
	http *http.Client
}

var _ provisioner.Driver = (*Driver)(nil)

// New builds the driver. A CA in cfg pins the API server.
func New(cfg Config) (*Driver, error) {
	if cfg.Host == "" || cfg.Namespace == "" {
		return nil, errors.New("k8s: Host and Namespace are required")
	}
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if cfg.CA != nil {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(cfg.CA) {
			return nil, errors.New("k8s: CA is not PEM")
		}
		tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	if cfg.TTLAfterFinished <= 0 {
		cfg.TTLAfterFinished = DefaultTTLAfterFinished
	}
	if cfg.Now == nil {
		cfg.Now = func() int64 { return time.Now().Unix() }
	}
	return &Driver{cfg: cfg, http: &http.Client{Transport: tr}}, nil
}

// jobName is the Handle: the lease id under a fixed prefix (a DNS
// label; lease ids are 16 hex).
func jobName(lease string) string { return "sap-" + lease }

func (d *Driver) jobsURL(name, query string) string {
	u := d.cfg.Host + "/apis/batch/v1/namespaces/" + url.PathEscape(d.cfg.Namespace) + "/jobs"
	if name != "" {
		u += "/" + url.PathEscape(name)
	}
	if query != "" {
		u += "?" + query
	}
	return u
}

// apiError is a non-2xx from the API server, with its Status message.
type apiError struct {
	Code int
	Msg  string
}

func (e *apiError) Error() string { return fmt.Sprintf("k8s: %d %s", e.Code, e.Msg) }

func (d *Driver) do(ctx context.Context, method, u string, contentType string, body []byte, out any) error {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return err
	}
	if d.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+d.cfg.Token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
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
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// job is the slice of batch/v1 Job this driver reads and writes.
type job struct {
	APIVersion string   `json:"apiVersion,omitempty"`
	Kind       string   `json:"kind,omitempty"`
	Metadata   metadata `json:"metadata"`
	Spec       jobSpec  `json:"spec"`
	Status     struct {
		StartTime  *time.Time `json:"startTime,omitempty"`
		Conditions []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"conditions,omitempty"`
	} `json:"status,omitempty"`
}

type metadata struct {
	Name              string            `json:"name,omitempty"`
	Namespace         string            `json:"namespace,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
	CreationTimestamp *time.Time        `json:"creationTimestamp,omitempty"`
}

type jobSpec struct {
	ActiveDeadlineSeconds   *int64 `json:"activeDeadlineSeconds,omitempty"`
	BackoffLimit            *int32 `json:"backoffLimit,omitempty"`
	TTLSecondsAfterFinished *int64 `json:"ttlSecondsAfterFinished,omitempty"`
	Template                struct {
		Spec podSpec `json:"spec"`
	} `json:"template"`
}

type podSpec struct {
	RestartPolicy                string          `json:"restartPolicy,omitempty"`
	AutomountServiceAccountToken *bool           `json:"automountServiceAccountToken,omitempty"`
	SecurityContext              json.RawMessage `json:"securityContext,omitempty"`
	Containers                   []container     `json:"containers"`
}

type container struct {
	Name            string          `json:"name"`
	Image           string          `json:"image"`
	Env             []envVar        `json:"env,omitempty"`
	SecurityContext json.RawMessage `json:"securityContext,omitempty"`
}

type envVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// PodSecurity `restricted` (the cluster's namespaces enforce it).
var (
	podSecurity       = json.RawMessage(`{"runAsNonRoot":true,"seccompProfile":{"type":"RuntimeDefault"}}`)
	containerSecurity = json.RawMessage(`{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]}}`)
)

// deadline is until as seconds from a start instant, floored at 1 (a
// zero or negative activeDeadlineSeconds is rejected by the API).
func deadline(until, from int64) int64 {
	if d := until - from; d > 0 {
		return d
	}
	return 1
}

func ptr[T any](v T) *T { return &v }

// Start creates the Job. The deadline runs from now under cfg.Now —
// the provisioner's clock — and the platform counts from its own
// startTime, a little later; the difference is slack in the child's
// favour, corrected at the first Extend.
func (d *Driver) Start(ctx context.Context, spec provisioner.StartSpec) (provisioner.Handle, error) {
	if err := provisioner.CheckImage(spec.Image); err != nil {
		return "", err
	}
	name := jobName(spec.Lease)
	j := job{APIVersion: "batch/v1", Kind: "Job", Metadata: metadata{
		Name:        name,
		Labels:      map[string]string{provisioner.LabelLease: spec.Lease},
		Annotations: map[string]string{provisioner.LabelOwner: string(spec.Owner)},
	}}
	j.Spec.ActiveDeadlineSeconds = ptr(deadline(spec.Until, d.cfg.Now()))
	j.Spec.BackoffLimit = ptr[int32](0)
	j.Spec.TTLSecondsAfterFinished = ptr(d.cfg.TTLAfterFinished)
	j.Spec.Template.Spec = podSpec{
		RestartPolicy:                "Never",
		AutomountServiceAccountToken: ptr(false),
		SecurityContext:              podSecurity,
		Containers: []container{{
			Name:            "actor",
			Image:           spec.Image,
			Env:             []envVar{{Name: ParamsEnv, Value: string(spec.Params)}},
			SecurityContext: containerSecurity,
		}},
	}
	body, err := json.Marshal(j)
	if err != nil {
		return "", err
	}
	if err := d.do(ctx, http.MethodPost, d.jobsURL("", ""), "application/json", body, nil); err != nil {
		return "", err
	}
	return provisioner.Handle(name), nil
}

// Extend sets activeDeadlineSeconds = until − startTime on the live
// Job (creationTimestamp when the controller has not stamped it yet).
func (d *Driver) Extend(ctx context.Context, h provisioner.Handle, until int64) error {
	var j job
	if err := d.do(ctx, http.MethodGet, d.jobsURL(string(h), ""), "", nil, &j); err != nil {
		return err
	}
	from := d.cfg.Now()
	switch {
	case j.Status.StartTime != nil:
		from = j.Status.StartTime.Unix()
	case j.Metadata.CreationTimestamp != nil:
		from = j.Metadata.CreationTimestamp.Unix()
	}
	patch, _ := json.Marshal(map[string]any{"spec": map[string]any{"activeDeadlineSeconds": deadline(until, from)}})
	return d.do(ctx, http.MethodPatch, d.jobsURL(string(h), ""), "application/merge-patch+json", patch, nil)
}

// Kill deletes the Job and its pods. A Job already gone is a
// successful kill.
func (d *Driver) Kill(ctx context.Context, h provisioner.Handle) error {
	body := []byte(`{"propagationPolicy":"Background"}`)
	err := d.do(ctx, http.MethodDelete, d.jobsURL(string(h), ""), "application/json", body, nil)
	var ae *apiError
	if errors.As(err, &ae) && ae.Code == http.StatusNotFound {
		return nil
	}
	return err
}

// List returns every Job carrying the lease label that has not
// finished. A Job without the owner annotation is reported with an
// empty Owner; the provisioner skips it.
func (d *Driver) List(ctx context.Context) ([]provisioner.Running, error) {
	var list struct {
		Items []job `json:"items"`
	}
	q := "labelSelector=" + url.QueryEscape(provisioner.LabelLease)
	if err := d.do(ctx, http.MethodGet, d.jobsURL("", q), "", nil, &list); err != nil {
		return nil, err
	}
	var out []provisioner.Running
	for _, j := range list.Items {
		if finished(j) {
			continue
		}
		r := provisioner.Running{
			Lease:  j.Metadata.Labels[provisioner.LabelLease],
			Owner:  cert.ActorID(j.Metadata.Annotations[provisioner.LabelOwner]),
			Handle: provisioner.Handle(j.Metadata.Name),
		}
		if cs := j.Spec.Template.Spec.Containers; len(cs) > 0 {
			r.Image = cs[0].Image
		}
		out = append(out, r)
	}
	return out, nil
}

func finished(j job) bool {
	for _, c := range j.Status.Conditions {
		if (c.Type == "Failed" || c.Type == "Complete") && c.Status == "True" {
			return true
		}
	}
	return false
}
