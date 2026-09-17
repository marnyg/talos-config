// Package issuer is the hub's Issuer actor (ADR-0018, ADR-0024): the
// wallet's hot key for membership.
//
// The hub process generates a random Ed25519 keypair at start — the
// hubkey — and holds no authority until the owner's wallet signs one
// speak-as cert to it:
//
//	{iss: wallet, aud: hubkey, can: speak-as,
//	 cav: {verbs: [member, invoke], groups: <all groups>, delegable: false},
//	 iat, exp: iat + 120 d}
//
// From then on the hubkey signs member certs (90 d) and invoke grants
// (7 d); a caller's bundle carries the speak-as so any verifier maps
// hubkey → wallet offline. The hub is never a root: a verifier holds
// its own consent to the WALLET and the speak-as is data.
//
// Nothing here is durable (invariant 2, actor-owned state): the key
// dies with the process, and a redeploy rotates it. What the dead key
// signed renews at the live one because #renew resolves the old
// issuer through the wallet's speak-as (protocol actor, 359.8.1).
//
// The unseal is two EIP-191 signatures from the same allowlisted wallet
// (decision talos-config-ce8): one over masterderive.MasterMessage for
// the nebula CA and the secrets seed (hubseal.go), one over this
// package's proposal — the speak-as cert's RFC 8785 canonical JSON.
// The proposal is what the /status page shows the owner to sign; it
// names the hubkey, so a phished copy is useless against any other
// process.
package issuer

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
)

// Lifetimes in seconds (ADR-0018 "Lifetimes"; runway.qnt).
const (
	// Day is the unit the page renders runway in.
	Day int64 = 24 * 60 * 60
	// SpeakAsTTL is the unseal speak-as lifetime: the longest a process
	// may hold authority before the wallet must act again.
	SpeakAsTTL int64 = 120 * Day
	// MemberTTL is a member cert's lifetime.
	MemberTTL int64 = 90 * Day
	// GrantTTL is an invoke grant's lifetime.
	GrantTTL int64 = 7 * Day
	// NagBefore is the seal threshold: with less than this left on the
	// speak-as the Issuer stops serving beats (ADR-0018 q8h — the nag IS
	// a seal), so no cert leaves with less than the member runway
	// behind it. NagBefore == member runway, zero margin, by design.
	NagBefore int64 = 30 * Day
)

// Verbs the speak-as delegates. Literal (ADR-0018 "Caveats are literal").
var speakAsVerbs = []string{string(cert.VerbMember), string(cert.VerbInvoke)}

var (
	// ErrSealed: the Issuer holds no live speak-as.
	ErrSealed = errors.New("issuer: sealed (no speak-as held)")
	// ErrNag: the speak-as has less than NagBefore left; beats stop
	// until the wallet re-unseals this process (ADR-0018 q8h).
	ErrNag = errors.New("issuer: speak-as within the nag window; re-unseal")
	// ErrNotAllowed: the signature recovers to no allowlisted wallet's
	// proposal.
	ErrNotAllowed = errors.New("issuer: signature matches no admin wallet's proposal")
	// ErrGroup: a requested group is outside the speak-as caveat.
	ErrGroup = errors.New("issuer: group not delegated by the speak-as")
	// ErrNodeID: a member id must be an ed: id (an iroh EndpointId).
	ErrNodeID = errors.New("issuer: member id must be an ed: actor id")
)

// Issuer is the hubkey and the actor around it. Construct with New,
// unseal with Unseal, then mint. Safe for concurrent use.
//
// The embedded Actor Listens for the life of the process (in-memory
// transport for the sibling actors, iroh later — talos-config-e8d);
// Unseal and re-unseal swap its authority set through actor.Hold, and
// every facet guards itself with Serving, so a sealed Issuer is one
// that refuses, not one that is absent.
type Issuer struct {
	signer cert.EdSigner
	groups []string     // the finite group list the speak-as may delegate
	clock  func() int64 // local clock, Unix seconds

	// Actor is the protocol runtime for hubkey: #renew is served from
	// here. Transport is whatever the caller wired (nil until 359.8.2.x).
	Actor *actor.Actor

	mu        sync.Mutex
	proposals map[cert.ActorID]cert.Cert // per wallet: the unsigned speak-as offered for signing
	speakAs   *cert.Cert                 // nil while sealed
	admitted  []cert.ActorID             // in-process siblings consented to for #mint-device
}

// New returns a sealed Issuer over a fresh random hubkey. groups is the
// closed group list (mesh-policy.yaml's vocabulary); the speak-as
// proposal delegates all of them. clock nil ⇒ time.Now.
func New(groups []string, t actor.Transport, clock func() int64) (*Issuer, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("issuer: generating hubkey: %w", err)
	}
	return NewWithKey(priv, groups, t, clock), nil
}

// NewWithKey is New over a caller-supplied key (tests; never a durable
// key in production — the point is that hubkey is per process).
func NewWithKey(priv ed25519.PrivateKey, groups []string, t actor.Transport, clock func() int64) *Issuer {
	if clock == nil {
		clock = func() int64 { return time.Now().Unix() }
	}
	s := cert.NewEdSigner(priv)
	a := actor.New(s, t)
	a.Clock = clock
	i := &Issuer{
		signer:    s,
		groups:    slices.Sorted(slices.Values(slices.Clone(groups))),
		clock:     clock,
		Actor:     a,
		proposals: make(map[cert.ActorID]cert.Cert),
	}
	// #renew serves only while unsealed and outside the nag window; the
	// actor's own handler does the cert work.
	renew := a.AcceptTable[actor.FacetRenew]
	a.AcceptTable[actor.FacetRenew] = func(ctx context.Context, inv *actor.Invocation) ([]byte, error) {
		if err := i.Serving(); err != nil {
			return nil, err
		}
		return renew(ctx, inv)
	}
	a.AcceptTable[FacetMintDevice] = i.mintDeviceHandler
	return i
}

// Listen runs the Issuer's actor on its transport until ctx ends. Safe
// to call before Unseal: the inbox refuses everything until a
// speak-as is held (no consents, and every facet checks Serving).
func (i *Issuer) Listen(ctx context.Context) error {
	if i.Actor.Transport == nil {
		return actor.ErrNoTransport
	}
	return i.Actor.Listen(ctx)
}

// ID is the hubkey as an actor id (ed:<hex>), the iroh EndpointId the
// hub's endpoint will have (ADR-0024).
func (i *Issuer) ID() cert.ActorID { return i.signer.ActorID() }

// Fingerprint is a short human-checkable form of the hubkey for the
// unseal page: the first 16 hex chars of the public key.
func (i *Issuer) Fingerprint() string {
	h := strings.TrimPrefix(string(i.ID()), "ed:")
	if len(h) > 16 {
		h = h[:16]
	}
	return h
}

// WalletID renders a lowercase 0x address as an eth: actor id.
func WalletID(addr string) cert.ActorID {
	return cert.ActorID("eth:" + strings.ToLower(strings.TrimSpace(addr)))
}

// Proposal returns the unsigned speak-as cert offered to wallet, and
// the exact message to sign (its canonical JSON). The first call for a
// wallet fixes iat/exp; later calls return the same proposal so a
// signature made against a rendered page still matches. A successful
// Unseal clears the proposals, so a re-unseal from the nag window is
// offered a fresh 120 d cert, never the stale one.
func (i *Issuer) Proposal(wallet cert.ActorID) (cert.Cert, string, error) {
	if err := wallet.Validate(); err != nil {
		return cert.Cert{}, "", err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	c, ok := i.proposals[wallet]
	if !ok {
		now := i.clock()
		c = cert.Cert{
			Iss: wallet,
			Aud: string(i.ID()),
			Can: cert.VerbSpeakAs,
			Cav: cert.Caveats{
				Verbs:     slices.Clone(speakAsVerbs),
				Groups:    slices.Clone(i.groups),
				Delegable: false,
			},
			Iat: now,
			Exp: now + SpeakAsTTL,
		}
		i.proposals[wallet] = c
	}
	canon, err := cert.CanonicalBytes(c)
	if err != nil {
		return cert.Cert{}, "", err
	}
	return c, string(canon), nil
}

// Unseal accepts an EIP-191 signature (0x-hex, r||s||v) over one of the
// proposals offered to wallets, verifies it, and holds the resulting
// speak-as. Returns the wallet that signed. Idempotent once unsealed
// (a second valid signature replaces the held speak-as — that is how
// a long-lived process leaves the nag window without a redeploy).
// A malformed allowlist entry is a configuration error and fails the
// whole unseal loudly rather than being skipped.
func (i *Issuer) Unseal(sigHex string, wallets []cert.ActorID) (cert.ActorID, error) {
	sig, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(sigHex), "0x"))
	if err != nil || len(sig) != 65 {
		return "", fmt.Errorf("issuer: malformed signature")
	}
	for _, w := range wallets {
		c, _, err := i.Proposal(w)
		if err != nil {
			return "", fmt.Errorf("issuer: allowlist entry %q: %w", w, err)
		}
		c.Sig = sig
		if cert.Verify(c) != nil {
			continue
		}
		return w, i.hold(c)
	}
	return "", ErrNotAllowed
}

// hold installs a verified speak-as and the consents that make hub
// facets the WALLET's facets (ADR-0024 F): hubkey consents to the wallet
// for #renew with target: wallet, delegable — the root of every member
// chain that reaches this hub. Grants the hub later issues name
// target: wallet too, so they survive hubkey rotation.
func (i *Issuer) hold(sa cert.Cert) error {
	now := i.clock()
	if sa.Exp <= now {
		return fmt.Errorf("issuer: speak-as already expired")
	}
	consent, err := cert.Sign(cert.Cert{
		Aud: string(sa.Iss),
		Can: cert.VerbInvoke,
		Cav: cert.Caveats{
			Target:    []cert.ActorID{sa.Iss},
			Facet:     []string{actor.FacetRenew},
			Delegable: true,
		},
		Iat: now,
		Exp: sa.Exp,
	}, i.signer)
	if err != nil {
		return fmt.Errorf("issuer: signing consent: %w", err)
	}
	siblings, err := i.siblingConsents(now, sa.Exp)
	if err != nil {
		return err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.speakAs = &sa
	i.Actor.Hold(append([]cert.Cert{consent}, siblings...), []cert.Cert{sa})
	i.proposals = make(map[cert.ActorID]cert.Cert)
	return nil
}

// SpeakAs returns the held speak-as, or nil while sealed.
func (i *Issuer) SpeakAs() *cert.Cert {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.speakAs == nil {
		return nil
	}
	c := *i.speakAs
	return &c
}

// Wallet is the principal this hubkey speaks for, "" while sealed.
func (i *Issuer) Wallet() cert.ActorID {
	if sa := i.SpeakAs(); sa != nil {
		return sa.Iss
	}
	return ""
}

// Runway is the seconds left on the held speak-as (≤ 0 ⇒ sealed or
// expired).
func (i *Issuer) Runway() int64 {
	sa := i.SpeakAs()
	if sa == nil {
		return 0
	}
	return sa.Exp - i.clock()
}

// Serving reports whether the Issuer may sign right now: unsealed and
// outside the nag window. nil ⇒ yes; ErrSealed / ErrNag otherwise.
func (i *Issuer) Serving() error {
	r := i.Runway()
	switch {
	case r <= 0:
		return ErrSealed
	case r < NagBefore:
		return ErrNag
	}
	return nil
}

// Kit is what a new member walks away with: its member cert, the
// invoke grant to the Owner's #renew facet (target: wallet), and the
// speak-as that resolves both certs' issuer. The member stores the
// speak-as with its chain (actor.Grants) so the receiver can resolve
// hubkey → wallet.
type Kit struct {
	Member     cert.Cert
	RenewGrant cert.Cert
	SpeakAs    cert.Cert
}

// Mint issues a member cert to node (an ed: id, the member's own
// NodeId — never derived from anything the hub holds, ADR-0015) with
// the durable name and groups, plus the #renew grant. groups must be
// within the speak-as caveat: the hub can only put a member in a group
// the wallet delegated.
func (i *Issuer) Mint(node cert.ActorID, name string, groups []string) (Kit, error) {
	if err := i.Serving(); err != nil {
		return Kit{}, err
	}
	if sch, err := node.Scheme(); err != nil || sch != "ed:" {
		return Kit{}, ErrNodeID
	}
	if err := node.Validate(); err != nil {
		return Kit{}, fmt.Errorf("%w: %v", ErrNodeID, err)
	}
	if name == "" {
		return Kit{}, fmt.Errorf("issuer: member needs a name")
	}
	sa := *i.SpeakAs()
	groups = slices.Sorted(slices.Values(slices.Clone(groups)))
	for _, g := range groups {
		if !slices.Contains(sa.Cav.Groups, g) {
			return Kit{}, fmt.Errorf("%w: %q", ErrGroup, g)
		}
	}
	now := i.clock()
	member, err := cert.Sign(cert.Cert{
		Aud: string(node),
		Can: cert.VerbMember,
		Cav: cert.Caveats{Name: name, Groups: groups, Delegable: false},
		Iat: now,
		Exp: now + MemberTTL,
	}, i.signer)
	if err != nil {
		return Kit{}, fmt.Errorf("issuer: signing member: %w", err)
	}
	grant, err := cert.Sign(cert.Cert{
		Aud: string(node),
		Can: cert.VerbInvoke,
		Cav: cert.Caveats{
			Target:    []cert.ActorID{sa.Iss},
			Facet:     []string{actor.FacetRenew},
			Delegable: false,
		},
		Iat: now,
		Exp: now + GrantTTL,
	}, i.signer)
	if err != nil {
		return Kit{}, fmt.Errorf("issuer: signing renew grant: %w", err)
	}
	return Kit{Member: member, RenewGrant: grant, SpeakAs: sa}, nil
}
