package relaygate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/postage"
)

// Prepare builds id's presentation of chain to relay, solving the
// stamp if any presented link demands postage (the newborn's wildcard
// link). scheme nil ⇒ postage.Default. The relay's own consent is not
// in chain and is assumed postage-free; if it is not, the gate refuses
// and says only "refused".
func Prepare(ctx context.Context, scheme postage.Scheme, relay, id cert.ActorID, chain []cert.Cert) (Presentation, error) {
	p := Presentation{ID: id, Chain: chain}
	var req string
	for _, c := range chain {
		if c.Cav.Postage != "" {
			req = c.Cav.Postage // monotone: the first link to set it wins the fold
			break
		}
	}
	if req == "" {
		return p, nil
	}
	if scheme == nil {
		scheme = postage.Default
	}
	stamp, err := scheme.Solve(ctx, req, StampPreimage(relay, id))
	if err != nil {
		return Presentation{}, fmt.Errorf("relaygate: stamp %s: %w", req, err)
	}
	p.Stamp = stamp
	return p, nil
}

// Post presents p at the gate in front of relayURL (the same URL the
// endpoint homes on) and returns the expiry the gate will hold the
// connection to. client nil ⇒ http.DefaultClient. Present again
// whenever the chain is renewed, and after any refusal by the relay:
// the gate's table is a cache and a restart empties it.
func Post(ctx context.Context, client *http.Client, relayURL string, p Presentation) (exp int64, err error) {
	body, err := EncodePresentation(p)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(relayURL, "/")+AdmitPath, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("relaygate: present: %w", err)
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
		var out struct {
			Exp int64 `json:"exp"`
		}
		if err := json.NewDecoder(io.LimitReader(res.Body, 1<<10)).Decode(&out); err != nil {
			return 0, fmt.Errorf("relaygate: present: reply: %w", err)
		}
		return out.Exp, nil
	case http.StatusForbidden:
		return 0, ErrRefused
	case http.StatusServiceUnavailable:
		return 0, ErrFull
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
		return 0, ErrBadPresentation
	}
	return 0, errors.New("relaygate: present: " + res.Status)
}
