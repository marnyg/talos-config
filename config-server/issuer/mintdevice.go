package issuer

// #mint-device: the Issuer's inbox for Enroll (ADR-0024). Enroll holds
// its own key and no wallet delegation; the Issuer consents to it at
// boot (Admit) for exactly this facet, and the REQUEST carries the
// wallet's approval — the EIP-191 signature over the v2 enrollment
// message naming the NodeId — which the Issuer verifies itself. A
// compromised Enroll key can therefore mint nothing without a wallet
// signature it does not have (ADR-0024 confirmation clause).
//
// Replay: the wallet's message binds a nonce, and Enroll's single-use
// nonce store is the replay check. The Issuer keeps none (invariant 2:
// it holds nothing durable, and a replayed approval re-mints the same
// (node, name, groups) to the same node — the node already holds it).
// Decision carried from ADR-0024's open item; the Issuer trusts
// Enroll's nonce for replay and nothing else.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/marnyg/talos-config/config-server/enrollmsg"
	"github.com/marnyg/talos-config/config-server/ethsig"
	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
)

// FacetMintDevice is the Issuer facet Enroll invokes.
const FacetMintDevice = "mint-device"

// ErrNotApproved: the request's wallet signature does not recover to
// the wallet this hubkey speaks for.
var ErrNotApproved = errors.New("issuer: approval not signed by the wallet this hub speaks for")

// MintDeviceRequest is #mint-device's payload: the approved enrollment
// and the wallet's signature over enrollmsg.V2(name, group, nebula
// fingerprint, node, nonce). Name is already normalized by Enroll;
// the Issuer rebuilds the message from these exact fields.
type MintDeviceRequest struct {
	Node        cert.ActorID `json:"node"`
	Name        string       `json:"name"`
	Group       string       `json:"group"`
	Fingerprint string       `json:"fingerprint"` // nebula pubkey fingerprint (v1 field)
	Nonce       string       `json:"nonce"`
	Signature   string       `json:"signature"` // 0x-hex EIP-191, r||s||v
}

// wireKit is Kit on the wire: each cert in its JSON form.
type wireKit struct {
	Member     json.RawMessage `json:"member"`
	RenewGrant json.RawMessage `json:"renew_grant"`
	SpeakAs    json.RawMessage `json:"speak_as"`
}

// EncodeKit renders a Kit as JSON.
func EncodeKit(k Kit) ([]byte, error) {
	var w wireKit
	var err error
	if w.Member, err = cert.Encode(k.Member); err != nil {
		return nil, err
	}
	if w.RenewGrant, err = cert.Encode(k.RenewGrant); err != nil {
		return nil, err
	}
	if w.SpeakAs, err = cert.Encode(k.SpeakAs); err != nil {
		return nil, err
	}
	return json.Marshal(w)
}

// DecodeKit parses EncodeKit's output, verifying each cert's signature.
func DecodeKit(b []byte) (Kit, error) {
	var w wireKit
	if err := json.Unmarshal(b, &w); err != nil {
		return Kit{}, fmt.Errorf("issuer: kit: %w", err)
	}
	var k Kit
	var err error
	if k.Member, err = cert.DecodeCert(w.Member); err != nil {
		return Kit{}, fmt.Errorf("issuer: kit member: %w", err)
	}
	if k.RenewGrant, err = cert.DecodeCert(w.RenewGrant); err != nil {
		return Kit{}, fmt.Errorf("issuer: kit renew grant: %w", err)
	}
	if k.SpeakAs, err = cert.DecodeCert(w.SpeakAs); err != nil {
		return Kit{}, fmt.Errorf("issuer: kit speak-as: %w", err)
	}
	for _, c := range []cert.Cert{k.Member, k.RenewGrant, k.SpeakAs} {
		if err := cert.Verify(c); err != nil {
			return Kit{}, fmt.Errorf("issuer: kit: %w", err)
		}
	}
	return k, nil
}

// Admit names an in-process sibling (Enroll) the Issuer will consent
// to for #mint-device. Takes effect at the next hold (unseal); calling
// it on an unsealed Issuer re-holds immediately. Idempotent.
func (i *Issuer) Admit(id cert.ActorID) error {
	if err := id.Validate(); err != nil {
		return fmt.Errorf("issuer: admit: %w", err)
	}
	i.mu.Lock()
	if !slices.Contains(i.admitted, id) {
		i.admitted = append(i.admitted, id)
	}
	sa := i.speakAs
	i.mu.Unlock()
	if sa != nil {
		return i.hold(*sa)
	}
	return nil
}

// siblingConsents signs the per-facet consents for every admitted
// sibling, bounded by the speak-as lifetime (a re-unseal re-signs
// them). target: hubkey is right here — Enroll and the Issuer are one
// process and die together, unlike member-held grants (ADR-0024 F).
func (i *Issuer) siblingConsents(now, exp int64) ([]cert.Cert, error) {
	i.mu.Lock()
	admitted := slices.Clone(i.admitted)
	i.mu.Unlock()
	out := make([]cert.Cert, 0, len(admitted))
	for _, id := range admitted {
		c, err := cert.Sign(cert.Cert{
			Aud: string(id),
			Can: cert.VerbInvoke,
			Cav: cert.Caveats{
				Target:    []cert.ActorID{i.ID()},
				Facet:     []string{FacetMintDevice},
				Delegable: false,
			},
			Iat: now,
			Exp: exp,
		}, i.signer)
		if err != nil {
			return nil, fmt.Errorf("issuer: signing sibling consent: %w", err)
		}
		out = append(out, c)
	}
	return out, nil
}

// mintDeviceHandler serves #mint-device. Authorization of the CALLER
// happened in the actor's inbox (the chain rooted in the sibling
// consent); this verifies the wallet's approval INSIDE the request.
func (i *Issuer) mintDeviceHandler(_ context.Context, inv *actor.Invocation) ([]byte, error) {
	if err := i.Serving(); err != nil {
		return nil, err
	}
	var req MintDeviceRequest
	if err := json.Unmarshal(inv.Envelope.Payload, &req); err != nil {
		return nil, fmt.Errorf("issuer: mint-device: %w", err)
	}
	if req.Name == "" || req.Group == "" || req.Fingerprint == "" || req.Nonce == "" || req.Signature == "" {
		return nil, errors.New("issuer: mint-device: node, name, group, fingerprint, nonce and signature are all required")
	}
	msg := enrollmsg.V2(req.Name, req.Group, req.Fingerprint, string(req.Node), req.Nonce)
	addr, err := ethsig.RecoverPersonalSign(msg, req.Signature)
	if err != nil {
		return nil, fmt.Errorf("issuer: mint-device: %w", err)
	}
	if WalletID(addr) != i.Wallet() {
		return nil, fmt.Errorf("%w (signed by %s)", ErrNotApproved, strings.ToLower(addr))
	}
	kit, err := i.Mint(req.Node, req.Name, []string{req.Group})
	if err != nil {
		return nil, err
	}
	return EncodeKit(kit)
}
