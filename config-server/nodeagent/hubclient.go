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

// --- receiver-held pins (protocol ADR-0010, d0o0) --------------------

// trivialPin is the node's commitment to its Owner's identity as a
// legacy EOA: the address is its own root and sole guardian at epoch
// 0, so Pin.Admits(address) holds until a rotation is folded.
func trivialPin(identity cert.ActorID) *cert.Pin {
	return cert.NewPin(identity, cert.Policy{Root: identity, Guardians: []cert.ActorID{identity}, K: 1})
}

// foldRotations returns a FRESH pin equal to pin with every rotation
// from a #bundle that names pin's address folded in: the hub's copy of
// the owner's rotation history, re-verified here against the node's
// own pin (cert.AssembleRotation checks the k guardian signatures and
// the body; Pin.Hold re-checks and folds). A new value, never a
// mutation: the installed pin is read concurrently by every Authorize,
// and cert.Pin is not safe for concurrent use — callers HoldPins the
// result. Rotations for other addresses are skipped; a rotation whose
// prev names no policy this pin knows (the epoch-0 policy, the current
// view, or any held rotation's next) cannot be assembled and is
// reported; an equivocation is reported and the pin is dead from then
// on (nothing under that identity verifies — the owner must re-pin out
// of band). now bounds assembly only (the certs' exp), never validity.
func foldRotations(pin *cert.Pin, rotations [][]cert.Cert, now int64, report func(error)) *cert.Pin {
	out := cert.NewPin(pin.Address, pin.Policy0)
	for _, r := range pin.Held() {
		if err := out.Hold(r); err != nil && !errors.Is(err, cert.ErrNotRooted) && !errors.Is(err, cert.ErrEquivocation) {
			report(fmt.Errorf("nodeagent: re-holding rotation epoch %d: %w", r.Epoch, err))
		}
	}
	for i, certs := range rotations {
		if len(certs) == 0 || certs[0].Aud != string(out.Address) {
			continue
		}
		head := certs[0]
		prev, ok := knownPolicy(out, head.Cav.Prev)
		if !ok {
			report(fmt.Errorf("nodeagent: bundle rotation %d (epoch %d): prev %.8s names no policy this pin knows", i, head.Cav.Epoch, head.Cav.Prev))
			continue
		}
		rot, err := cert.AssembleRotation(out.Address, prev, head.Cav.Epoch, certs, now)
		if err != nil {
			report(fmt.Errorf("nodeagent: bundle rotation %d (epoch %d): %w", i, head.Cav.Epoch, err))
			continue
		}
		if err := out.Hold(rot); err != nil {
			report(fmt.Errorf("nodeagent: bundle rotation %d (epoch %d): %w", i, head.Cav.Epoch, err))
		}
	}
	return out
}

// knownPolicy finds the policy with the given hash among those the pin
// can root a transition from: epoch 0, the current view, and every
// held rotation's next (an out-of-order arrival chains later).
func knownPolicy(pin *cert.Pin, hash string) (cert.Policy, bool) {
	cands := []cert.Policy{pin.Policy0, pin.View().Policy}
	for _, r := range pin.Held() {
		cands = append(cands, r.Next)
	}
	for _, p := range cands {
		if p.Hash() == hash {
			return p, true
		}
	}
	return cert.Policy{}, false
}
