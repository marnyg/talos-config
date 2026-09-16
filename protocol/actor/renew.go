package actor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/envelope"
)

// FacetRenew is the renewal facet every actor exposes. It is an
// ORDINARY facet behind an ordinary grant (ruling 2026-09-12, 0bc.2):
// the caller must hold a chain to (me, "#renew") like for any facet;
// "any cert I signed" never authorises asking.
const FacetRenew = "#renew"

// RenewRequest is the #renew payload: a batch of certs this actor
// issued (directly or through one of its hot keys) that the caller
// wants re-signed with fresh iat/exp.
type RenewRequest struct {
	Items []RenewItem `json:"items"`
}

// RenewItem is one cert to renew. Cert is the wire JSON (cert.Encode)
// of the currently held cert. Want, optional, is an UNSIGNED cert wire
// JSON proposing a same-or-narrower replacement (fewer targets/facets,
// delegable dropped, …); iss/iat/exp/sig in Want are ignored. Absent ⇒
// re-issue with the same caveats.
type RenewItem struct {
	Cert json.RawMessage `json:"cert"`
	Want json.RawMessage `json:"want,omitempty"`
}

// RenewResponse is the #renew reply body: one result per item, same
// order.
type RenewResponse struct {
	Results []RenewResult `json:"results"`
}

// RenewResult is the per-cert outcome: the new cert's wire JSON, or a
// refusal reason.
type RenewResult struct {
	Cert  json.RawMessage `json:"cert,omitempty"`
	Error string          `json:"error,omitempty"`
}

// Refusal reasons a grantor puts in RenewResult.Error.
const (
	RefuseNotMine   = "not issued by this actor"
	RefuseExpired   = "expired"
	RefuseAud       = "aud is neither the caller nor a principal it speaks for"
	RefuseWider     = "requested caveats are not same-or-narrower"
	RefuseMalformed = "malformed"
)

// EncodeRenewRequest renders held certs (and optional narrower
// proposals, positionally; nil for none) as a #renew payload.
func EncodeRenewRequest(certs []cert.Cert, want []*cert.Cert) ([]byte, error) {
	req := RenewRequest{Items: make([]RenewItem, len(certs))}
	for i, c := range certs {
		raw, err := cert.Encode(c)
		if err != nil {
			return nil, err
		}
		req.Items[i].Cert = raw
		if i < len(want) && want[i] != nil {
			w, err := cert.Encode(*want[i])
			if err != nil {
				return nil, err
			}
			req.Items[i].Want = w
		}
	}
	return json.Marshal(req)
}

// DecodeRenewResponse parses a #renew reply body into per-item results:
// a decoded cert or an error, same order as the request.
func DecodeRenewResponse(body []byte) ([]cert.Cert, []error, error) {
	var resp RenewResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, nil, fmt.Errorf("actor: renew response: %w", err)
	}
	certs := make([]cert.Cert, len(resp.Results))
	errs := make([]error, len(resp.Results))
	for i, r := range resp.Results {
		if r.Error != "" {
			errs[i] = errors.New(r.Error)
			continue
		}
		c, err := cert.DecodeCert(r.Cert)
		if err != nil {
			errs[i] = err
			continue
		}
		certs[i] = c
	}
	return certs, errs, nil
}

// renewHandler serves FacetRenew. Per item, in order:
//
//  1. decode the held cert; refuse malformed
//  2. own-signature: cert.Verify ok AND its iss is this actor OR a hot
//     key this actor vouches for through a live speak-as in a.SpeakAs
//     (iss == me, aud == cert.iss, cav.verbs ∋ cert.can)
//  3. unexpired at the effective clock
//  4. aud binds the invocation's From the way VerifyChain rule 3 binds
//     a chain's last link: aud == From, or aud == P with a live speak-as
//     P→From in the proof whose cav.verbs ∋ invoke (cert.SpeaksFor) —
//     so a holder whose cert names its cold principal renews it from
//     its hot key (talos-config-7ei). The caller renews its own certs;
//     renewing on behalf of a third party is a new negotiation
//  5. build the replacement: same aud/can, caveats = held (or Want if
//     cert.Attenuate(held, want) == want, i.e. same-or-narrower —
//     never wider), iat = now, exp = now + lifetime, signed by a.Signer
//
// The reply, like every reply, carries this actor's current reach-me-at
// so a holder whose grantor moved learns the new location on the beat.
func (a *Actor) renewHandler(_ context.Context, inv *Invocation) ([]byte, error) {
	var req RenewRequest
	if err := json.Unmarshal(inv.Envelope.Payload, &req); err != nil {
		return nil, fmt.Errorf("renew: payload: %w", err)
	}
	now := a.Now()
	// the proof's speak-as certs — the same set the chain bound aud with
	_, proofSpeakAs := envelope.SplitProof(inv.Envelope.Proof)
	resp := RenewResponse{Results: make([]RenewResult, len(req.Items))}
	for i, it := range req.Items {
		resp.Results[i] = a.renewOne(it, inv.From, proofSpeakAs, now)
	}
	return json.Marshal(resp)
}

func (a *Actor) renewOne(it RenewItem, caller cert.ActorID, proofSpeakAs []cert.Cert, now int64) RenewResult {
	refuse := func(why string) RenewResult { return RenewResult{Error: why} }
	old, err := cert.DecodeCert(it.Cert)
	if err != nil {
		return refuse(RefuseMalformed + ": " + err.Error())
	}
	if !a.issuedByMe(old, now) {
		return refuse(RefuseNotMine)
	}
	if old.Exp <= now {
		return refuse(RefuseExpired)
	}
	if old.Aud != string(caller) &&
		!cert.SpeaksFor(cert.ActorID(old.Aud), caller, cert.VerbInvoke, proofSpeakAs, now) {
		return refuse(RefuseAud)
	}
	fresh := old
	if len(it.Want) > 0 {
		want, err := cert.DecodeCert(it.Want)
		if err != nil {
			return refuse(RefuseMalformed + ": want: " + err.Error())
		}
		if want.Aud != old.Aud || want.Can != old.Can || !narrower(old, want) {
			return refuse(RefuseWider)
		}
		fresh.Cav = want.Cav
	}
	ttl := a.RenewTTL
	if ttl <= 0 {
		ttl = old.Exp - old.Iat
	}
	if ttl <= 0 {
		return refuse(RefuseMalformed + ": non-positive lifetime")
	}
	fresh.Iat = now
	fresh.Exp = now + ttl
	fresh.Sig = nil
	signed, err := cert.Sign(fresh, a.Signer) // sets iss = me
	if err != nil {
		return refuse("sign: " + err.Error())
	}
	raw, err := cert.Encode(signed)
	if err != nil {
		return refuse("encode: " + err.Error())
	}
	return RenewResult{Cert: raw}
}

// issuedByMe is the own-signature check with hot-key resolution: the
// cert verifies under its iss, and iss is this actor or a key one of
// this actor's own speak-as certs (iss == me, live at now, covering the
// cert's verb) names as aud.
func (a *Actor) issuedByMe(c cert.Cert, now int64) bool {
	if cert.Verify(c) != nil {
		return false
	}
	me := a.ID()
	if c.Iss == me {
		return true
	}
	for _, s := range a.SpeakAs {
		if s.Can != cert.VerbSpeakAs || s.Iss != me || s.Aud != string(c.Iss) {
			continue
		}
		if s.Exp <= now || !containsStr(s.Cav.Verbs, string(c.Can)) {
			continue
		}
		if cert.Verify(s) == nil {
			return true
		}
	}
	return false
}

// narrower reports whether want's caveats are the same as or narrower
// than old's, using cert.Attenuate as the single definition of
// "narrower": fold old over want and require the result to BE want
// (target/facet/groups/verbs/endpoints intersections unchanged, postage
// carried forward unchanged, no taint). Delegability is checked apart
// because Attenuate refuses a non-delegable parent outright, and time
// fields are neutralised because this is not a chain link.
func narrower(old, want cert.Cert) bool {
	if want.Cav.Delegable && !old.Cav.Delegable {
		return false
	}
	if want.Cav.Unknown {
		return false
	}
	parent := old
	parent.Cav.Delegable = true
	parent.Iat, parent.Exp = want.Iat, want.Exp
	eff, err := cert.Attenuate(parent, want)
	if err != nil || eff.Cav.Unknown {
		return false
	}
	eff.Iss, want.Iss = "", ""
	eff.Sig, want.Sig = nil, nil
	a, err1 := cert.CanonicalBytes(eff)
	b, err2 := cert.CanonicalBytes(want)
	return err1 == nil && err2 == nil && bytes.Equal(a, b)
}

func containsStr(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
