// Package boottoken is the ADR-0015 boot-enrollment token: what a
// served machine config carries INSTEAD of a private key, redeemable
// once for a member cert minted to the NodeId the machine minted
// itself.
//
// Shape (approval.qnt, decision vzj): HMAC-derived from the master so
// verification is stateless — no pending table, any hub process
// holding the master verifies any token; bound to the MAC so the cert
// it buys carries that MAC's git-declared name; TTL-bounded; carrying
// a random nonce so two serves in one second are two tokens (a
// wipe-and-reboot inside the TTL must not collide with the volatile
// replay guard). Single-use PER HUB PROCESS: Seen is the guard, and it
// dies with the process — a token copied from a served config replays
// after a redeploy inside its TTL. Accepted residual: strictly less
// exposure than the config that used to carry the key itself.
//
// The token never reaches git or a log; it lives in the served config
// (Talos stores it in STATE) and in the agent's memory while redeeming.
package boottoken

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/marnyg/talos-config/config-server/masterderive"
)

// TTL is how long after serve a token redeems. A blank machine installs,
// reboots and starts the agent in minutes; an hour covers a slow KMS
// unseal or a second boot without leaving a stale config redeemable
// for long. A node re-enrolling later (Kit lost AND expired) needs a
// fresh serve (`nix run .#apply`).
const TTL = time.Hour

// Skew tolerates a hub clock ahead of the one that minted (a redeploy
// onto a different host inside the TTL).
const Skew = 2 * time.Minute

const prefix = "bt1."

var (
	// ErrMalformed: not a token this package minted.
	ErrMalformed = errors.New("boottoken: malformed")
	// ErrSignature: HMAC mismatch — a different master, or forged.
	ErrSignature = errors.New("boottoken: bad signature")
	// ErrExpired: outside [iat - Skew, iat + TTL].
	ErrExpired = errors.New("boottoken: expired")
	// ErrReplayed: already redeemed by this hub process.
	ErrReplayed = errors.New("boottoken: already redeemed")
)

type payload struct {
	MAC   string `json:"mac"`
	Iat   int64  `json:"iat"`
	Nonce string `json:"nonce"`
}

var b64 = base64.RawURLEncoding

// Mint issues a token for mac (normalized, dashed) at now.
func Mint(master []byte, mac string, now time.Time) (string, error) {
	if mac == "" {
		return "", errors.New("boottoken: empty mac")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	body, err := json.Marshal(payload{MAC: mac, Iat: now.Unix(), Nonce: b64.EncodeToString(nonce[:])})
	if err != nil {
		return "", err
	}
	return prefix + b64.EncodeToString(body) + "." + b64.EncodeToString(sign(master, body)), nil
}

func sign(master, body []byte) []byte {
	h := hmac.New(sha256.New, masterderive.BootTokenKey(master))
	h.Write(body)
	return h.Sum(nil)
}

// Verify checks the signature and the TTL and returns the MAC the
// token was minted for. It does not consult the replay guard — see
// Seen.Use.
func Verify(master []byte, token string, now time.Time) (mac string, err error) {
	rest, ok := strings.CutPrefix(token, prefix)
	if !ok {
		return "", ErrMalformed
	}
	bodyB64, sigB64, ok := strings.Cut(rest, ".")
	if !ok {
		return "", ErrMalformed
	}
	body, err := b64.DecodeString(bodyB64)
	if err != nil {
		return "", ErrMalformed
	}
	sig, err := b64.DecodeString(sigB64)
	if err != nil {
		return "", ErrMalformed
	}
	if !hmac.Equal(sig, sign(master, body)) {
		return "", ErrSignature
	}
	var p payload
	if err := json.Unmarshal(body, &p); err != nil || p.MAC == "" || p.Nonce == "" {
		return "", ErrMalformed
	}
	iat := time.Unix(p.Iat, 0)
	if now.Before(iat.Add(-Skew)) || now.After(iat.Add(TTL)) {
		return "", fmt.Errorf("%w: minted %s", ErrExpired, iat.UTC().Format(time.RFC3339))
	}
	return p.MAC, nil
}

// Seen is the volatile single-use guard: tokens this process redeemed.
// Safe to lose (invariant 2): losing it degrades to the TTL bound,
// never to a stronger grant. Entries fall out once they can no longer
// verify anyway.
type Seen struct {
	mu   sync.Mutex
	used map[string]time.Time // token → when it stops being verifiable
}

// Use records token as redeemed. It returns ErrReplayed if this process
// already redeemed it. Call only after Verify succeeded.
func (s *Seen) Use(token string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used == nil {
		s.used = map[string]time.Time{}
	}
	for t, until := range s.used {
		if now.After(until) {
			delete(s.used, t)
		}
	}
	if _, dup := s.used[token]; dup {
		return ErrReplayed
	}
	s.used[token] = now.Add(TTL + Skew)
	return nil
}

// Release forgets a token Use recorded, when what it was used for did
// not happen (the mint failed after the guard passed).
func (s *Seen) Release(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.used, token)
}
