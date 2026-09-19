package nodeagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/marnyg/talos-config/config-server/issuer"
	"github.com/marnyg/talos-config/protocol/cert"
)

// Well-known paths the hub serves its cold-cache certs on (hubseal.go).
const (
	WellKnownSpeakAs   = "/.well-known/talos-hub/speak-as"
	WellKnownReachMeAt = "/.well-known/talos-hub/reach-me-at"
)

// ErrTokenDead: the hub rejected the token for good (401/409). Only a
// fresh config serve helps; retrying is pointless.
var ErrTokenDead = errors.New("nodeagent: boot token rejected; the node needs a fresh config serve")

// ErrHubSealed: the hub cannot answer yet (503); retry later.
var ErrHubSealed = errors.New("nodeagent: hub sealed")

// FetchHub GETs the hub's current speak-as and reach-me-at. wallet, when
// set, is the sovereign the agent enrolled under: a speak-as from any
// other issuer is refused — web PKI names a host, the wallet names the
// authority (decision bjg: the first fetch trusts web PKI; after
// enrollment the Kit's speak-as issuer pins it).
func FetchHub(ctx context.Context, c *http.Client, base string, wallet cert.ActorID) (HubRecord, error) {
	sa, err := fetchCert(ctx, c, base+WellKnownSpeakAs)
	if err != nil {
		return HubRecord{}, err
	}
	if sa.Can != cert.VerbSpeakAs {
		return HubRecord{}, fmt.Errorf("nodeagent: %s: verb %q", WellKnownSpeakAs, sa.Can)
	}
	if wallet != "" && sa.Iss != wallet {
		return HubRecord{}, fmt.Errorf("nodeagent: hub speak-as from %s, enrolled under %s", sa.Iss, wallet)
	}
	loc, err := fetchCert(ctx, c, base+WellKnownReachMeAt)
	if err != nil {
		return HubRecord{}, err
	}
	if loc.Can != cert.VerbReachMeAt || loc.Iss != cert.ActorID(sa.Aud) {
		return HubRecord{}, fmt.Errorf("nodeagent: %s: not the hubkey's location (%s vs %s)", WellKnownReachMeAt, loc.Iss, sa.Aud)
	}
	return HubRecord{SpeakAs: sa, ReachMeAt: loc}, nil
}

func fetchCert(ctx context.Context, c *http.Client, url string) (cert.Cert, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return cert.Cert{}, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return cert.Cert{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return cert.Cert{}, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusServiceUnavailable:
		return cert.Cert{}, fmt.Errorf("%w: %s: %s", ErrHubSealed, url, strings.TrimSpace(string(body)))
	default:
		return cert.Cert{}, fmt.Errorf("nodeagent: %s: %d %s", url, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	cc, err := cert.DecodeCert(body)
	if err != nil {
		return cert.Cert{}, fmt.Errorf("nodeagent: %s: %w", url, err)
	}
	if err := cert.Verify(cc); err != nil {
		return cert.Cert{}, fmt.Errorf("nodeagent: %s: %w", url, err)
	}
	return cc, nil
}

// Enroll redeems the boot token for node's Kit (POST EnrollPath). The
// Kit's certs are verified: member to node, beat grant to node, and a
// speak-as that resolves both issuers.
func Enroll(ctx context.Context, c *http.Client, base string, node cert.ActorID, token string) (issuer.Kit, error) {
	body, err := json.Marshal(EnrollRequest{Node: node, Token: token})
	if err != nil {
		return issuer.Kit{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+EnrollPath, bytes.NewReader(body))
	if err != nil {
		return issuer.Kit{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return issuer.Kit{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return issuer.Kit{}, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusConflict:
		return issuer.Kit{}, fmt.Errorf("%w (%d %s)", ErrTokenDead, resp.StatusCode, strings.TrimSpace(string(raw)))
	case http.StatusServiceUnavailable:
		return issuer.Kit{}, fmt.Errorf("%w: %s", ErrHubSealed, strings.TrimSpace(string(raw)))
	default:
		return issuer.Kit{}, fmt.Errorf("nodeagent: enroll: %d %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	kit, err := issuer.DecodeKit(raw)
	if err != nil {
		return issuer.Kit{}, fmt.Errorf("nodeagent: enroll: %w", err)
	}
	if err := CheckKit(kit, node); err != nil {
		return issuer.Kit{}, err
	}
	return kit, nil
}

// CheckKit verifies a Kit is node's: signatures, verbs, audiences, and
// that the speak-as names the issuer of both certs.
func CheckKit(k issuer.Kit, node cert.ActorID) error {
	for _, c := range []cert.Cert{k.Member, k.BeatGrant, k.SpeakAs} {
		if err := cert.Verify(c); err != nil {
			return fmt.Errorf("nodeagent: kit: %w", err)
		}
	}
	switch {
	case k.Member.Can != cert.VerbMember || k.Member.Aud != string(node):
		return fmt.Errorf("nodeagent: kit: member cert is not ours")
	case k.BeatGrant.Can != cert.VerbInvoke || k.BeatGrant.Aud != string(node):
		return fmt.Errorf("nodeagent: kit: beat grant is not ours")
	case k.SpeakAs.Can != cert.VerbSpeakAs || k.SpeakAs.Aud != string(k.Member.Iss) || k.Member.Iss != k.BeatGrant.Iss:
		return fmt.Errorf("nodeagent: kit: speak-as does not resolve the issuer")
	}
	return nil
}
