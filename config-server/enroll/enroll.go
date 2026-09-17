// Package enroll is the hub's Enroll actor (ADR-0024): the device-flow
// side of membership. It holds its own per-process Ed25519 key and no
// wallet delegation; the Issuer consents to it for exactly one facet
// (#mint-device), and every request Enroll sends carries the wallet's
// approval signature, which the Issuer verifies itself. Enroll is
// therefore a courier with a key, not an authority: kill it mid-flight
// and only that flight is lost; steal its key and nothing mints without
// a wallet signature.
//
// The WAN HTTPS handlers (device flow, wallet approval, nebenroll.go)
// are unchanged and stay in package main; they call MintDevice once the
// wallet has approved an enrollment that named a NodeId. Enroll speaks
// to the Issuer over the protocol's in-memory transport, so promoting
// it to its own process is a transport swap, not a redesign.
package enroll

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"

	"github.com/marnyg/talos-config/config-server/issuer"
	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
)

// Enroll is the actor. Construct with New; it does not Listen (it has
// no inbox in v0 — domain model §2), it only Sends.
type Enroll struct {
	signer cert.EdSigner
	actor  *actor.Actor
	issuer cert.ActorID
}

// New binds a fresh per-process key on net and points it at the Issuer
// identified by iss. The caller must Issuer.Admit(e.ID()) so the
// Issuer's inbox consents to this key.
func New(net *actor.MemoryNetwork, iss cert.ActorID, clock func() int64) (*Enroll, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("enroll: generating key: %w", err)
	}
	s := cert.NewEdSigner(priv)
	// Named by key: the memory network's names are unique per binding,
	// and nothing dials Enroll by name.
	ep, err := net.Bind(s.ActorID(), "enroll-"+string(s.ActorID())[3:19])
	if err != nil {
		return nil, fmt.Errorf("enroll: %w", err)
	}
	a := actor.New(s, ep)
	if clock != nil {
		a.Clock = clock
	}
	return &Enroll{signer: s, actor: a, issuer: iss}, nil
}

// ID is Enroll's actor id (ed:<hex>).
func (e *Enroll) ID() cert.ActorID { return e.signer.ActorID() }

// MintDevice asks the Issuer for a member kit for an approved
// enrollment. The chain Enroll presents is empty: it IS the consented
// principal (the Issuer's sibling consent names its key as aud), so the
// receiver's own consent roots the proof. The Issuer re-verifies the
// wallet's signature inside req before minting.
func (e *Enroll) MintDevice(ctx context.Context, req issuer.MintDeviceRequest) (issuer.Kit, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return issuer.Kit{}, fmt.Errorf("enroll: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rep, err := e.actor.Send(ctx, e.issuer, issuer.FacetMintDevice, payload)
	if err != nil {
		return issuer.Kit{}, fmt.Errorf("enroll: mint-device: %w", err)
	}
	return issuer.DecodeKit(rep.Payload)
}
