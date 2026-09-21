// Package postage is the sovereign-actor protocol's stamp: the cost a
// STRANGER attaches so that unsolicited delivery costs the sender more
// than it costs the receiver (protocol ADR-0007; sketch § "Public
// reachability is a default, revocable facet").
//
// The protocol's authority layer already knows postage as a caveat:
// cav.postage is an opaque requirement string whose presence is what
// lets a chain end in aud "*" (cert.VerifyChain rule 3). This package
// gives that string a vocabulary and a Scheme that turns a requirement
// plus an envelope preimage into a token — and checks one. Settlement
// is pluggable by design: proof-of-work is the v0 placeholder, real
// micropayments are the goal (goals.md § v0 scope; open problem 1).
//
// # Vocabulary (v0)
//
//	pow:<bits>   SHA-256(preimage ‖ nonce) has ≥ bits leading zero bits;
//	             token = lowercase hex nonce; 0 < bits ≤ MaxPoWBits.
//
// A requirement whose scheme this verifier does not know REJECTS (fail
// closed, invariant 5) — an unknown stamp is no stamp.
//
// # What a token is bound to
//
// The preimage is envelope.PostagePreimage: the envelope's canonical
// form with sig and postage blanked. A token is thus good for exactly
// one envelope content. Re-sending that envelope is a replay the
// receiver's seq high-water mark catches for as long as it remembers
// the sender; a stranger with a fresh key pays afresh. There is no
// spent-token set (open problem 8: per-correspondent state at a popular
// frontdoor is unexamined).
package postage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/bits"
	"strconv"
	"strings"
)

// MaxPoWBits bounds a pow requirement: 2^64 expected hashes is beyond
// any honest sender; refusing larger keeps a hostile frontdoor cert
// from hanging a solver forever.
const MaxPoWBits = 64

var (
	// ErrUnknownScheme marks a requirement whose scheme is not in the
	// vocabulary. Fail closed: the receiver rejects, the sender refuses
	// to send.
	ErrUnknownScheme = errors.New("postage: unknown requirement scheme")
	// ErrMalformed marks a requirement or token that does not parse.
	ErrMalformed = errors.New("postage: malformed")
	// ErrInsufficient marks a token that does not meet the requirement.
	ErrInsufficient = errors.New("postage: token does not meet requirement")
	// ErrMissing marks an absent token where the requirement demands one.
	ErrMissing = errors.New("postage: no token")
)

// Scheme is a pluggable postage mechanism: Solve produces a token for
// req bound to preimage (the sender side; may be slow, honours ctx),
// Check verifies one (the receiver side; MUST be one cheap operation —
// the fee check itself must never become the DoS vector).
type Scheme interface {
	Solve(ctx context.Context, req string, preimage []byte) (token string, err error)
	Check(req string, preimage []byte, token string) error
}

// PoW is the proof-of-work Scheme for `pow:<bits>` requirements.
type PoW struct{}

// Default is the v0 scheme every actor uses unless configured otherwise.
var Default Scheme = PoW{}

// Require renders a pow requirement string for a frontdoor caveat.
func Require(bits int) string { return "pow:" + strconv.Itoa(bits) }

// ParsePoW reads `pow:<bits>`; ErrUnknownScheme for another scheme,
// ErrMalformed for a bad bit count.
func ParsePoW(req string) (int, error) {
	s, ok := strings.CutPrefix(req, "pow:")
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrUnknownScheme, req)
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 || n > MaxPoWBits {
		return 0, fmt.Errorf("%w: pow bits %q", ErrMalformed, s)
	}
	return n, nil
}

// leadingZeroBits of SHA-256(preimage ‖ nonce).
func leadingZeroBits(preimage, nonce []byte) int {
	h := sha256.New()
	h.Write(preimage)
	h.Write(nonce)
	sum := h.Sum(nil)
	n := 0
	for _, b := range sum {
		if b != 0 {
			return n + bits.LeadingZeros8(b)
		}
		n += 8
	}
	return n
}

// Solve grinds a random-seeded 8-byte nonce until the digest has the
// required zero bits. Expected 2^bits hashes; ctx cancels.
func (PoW) Solve(ctx context.Context, req string, preimage []byte) (string, error) {
	want, err := ParsePoW(req)
	if err != nil {
		return "", err
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("postage: nonce: %w", err)
	}
	n := binary.BigEndian.Uint64(nonce[:])
	for i := 0; ; i++ {
		if i&0xffff == 0 && ctx.Err() != nil {
			return "", ctx.Err()
		}
		binary.BigEndian.PutUint64(nonce[:], n)
		if leadingZeroBits(preimage, nonce[:]) >= want {
			return hex.EncodeToString(nonce[:]), nil
		}
		n++
	}
}

// Check is one SHA-256: the token is a hex nonce and the digest must
// carry the required zero bits.
func (PoW) Check(req string, preimage []byte, token string) error {
	want, err := ParsePoW(req)
	if err != nil {
		return err
	}
	if token == "" {
		return ErrMissing
	}
	nonce, err := hex.DecodeString(token)
	if err != nil || len(nonce) == 0 {
		return fmt.Errorf("%w: token %q", ErrMalformed, token)
	}
	if got := leadingZeroBits(preimage, nonce); got < want {
		return fmt.Errorf("%w: %d < %d bits", ErrInsufficient, got, want)
	}
	return nil
}
