package meshtoken

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/marnyg/talos-config/protocol/cert"
)

func newKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

var (
	now    = time.Unix(1_800_000_000, 0)
	caller = cert.Identity{
		Key:    cert.NewEdSigner(ed25519.NewKeyFromSeed(make([]byte, 32))).ActorID(),
		Name:   "marius-mac",
		Groups: []string{"admins"},
	}
)

func TestRoundTrip(t *testing.T) {
	s := NewSigner(newKey(t))
	v, err := NewVerifier(s.ID())
	if err != nil {
		t.Fatal(err)
	}
	tok, err := s.Mint(caller, "Sonarr.gw.mesh.internal:80", now)
	if err != nil {
		t.Fatal(err)
	}
	c, err := v.Verify(tok, "sonarr.gw.mesh.internal", now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if c.Issuer != s.ID() || c.Subject != caller.Key || c.Name != "marius-mac" || !c.HasGroup("admins") || c.HasGroup("media") {
		t.Errorf("claims = %+v", c)
	}
	if c.Audience != "sonarr.gw.mesh.internal" {
		t.Errorf("aud = %q", c.Audience)
	}
	if c.Expires-c.IssuedAt != int64(TTL.Seconds()) {
		t.Errorf("exp-iat = %d", c.Expires-c.IssuedAt)
	}
}

func TestRefusals(t *testing.T) {
	s := NewSigner(newKey(t))
	other := NewSigner(newKey(t))
	v, _ := NewVerifier(s.ID())
	tok, _ := s.Mint(caller, "sonarr.gw.mesh.internal", now)

	// A token from a key that is not pinned, even if well-formed.
	foreign, _ := other.Mint(caller, "sonarr.gw.mesh.internal", now)

	// Same body, signature from the wrong key: the iss names s but the
	// signature is other's.
	parts := strings.Split(tok, ".")
	fsig := ed25519.Sign(other.priv, []byte(parts[0]+"."+parts[1]))
	forged := parts[0] + "." + parts[1] + "." + base64.RawURLEncoding.EncodeToString(fsig)

	// Body tampered (groups widened) after signing.
	body, _ := base64.RawURLEncoding.DecodeString(parts[1])
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString(
		[]byte(strings.Replace(string(body), `"admins"`, `"admins","media"`, 1))) + "." + parts[2]

	// alg confusion: a header claiming another algorithm is malformed,
	// not negotiated.
	rs := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + parts[1] + "." + parts[2]

	cases := []struct {
		name string
		tok  string
		aud  string
		at   time.Time
		want error
	}{
		{"ok", tok, "sonarr.gw.mesh.internal", now, nil},
		{"foreign issuer", foreign, "sonarr.gw.mesh.internal", now, ErrIssuer},
		{"forged signature", forged, "sonarr.gw.mesh.internal", now, ErrSignature},
		{"tampered body", tampered, "sonarr.gw.mesh.internal", now, ErrSignature},
		{"alg confusion", rs, "sonarr.gw.mesh.internal", now, ErrMalformed},
		{"garbage", "a.b", "sonarr.gw.mesh.internal", now, ErrMalformed},
		{"empty", "", "sonarr.gw.mesh.internal", now, ErrMalformed},
		{"other host", tok, "auth.gw.mesh.internal", now, ErrAudience},
		{"expired", tok, "sonarr.gw.mesh.internal", now.Add(TTL + Leeway + time.Second), ErrExpired},
		{"within leeway", tok, "sonarr.gw.mesh.internal", now.Add(TTL + Leeway - time.Second), nil},
		{"from the future", tok, "sonarr.gw.mesh.internal", now.Add(-Leeway - time.Second), ErrExpired},
	}
	for _, tc := range cases {
		_, err := v.Verify(tc.tok, tc.aud, tc.at)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestVerifierPins(t *testing.T) {
	if _, err := NewVerifier(); err == nil {
		t.Error("empty pin set accepted")
	}
	if _, err := NewVerifier(cert.ActorID("eth:0x" + strings.Repeat("ab", 20))); err == nil {
		t.Error("eth id accepted as a token issuer")
	}
	if _, err := NewVerifier(cert.ActorID("ed:zz")); err == nil {
		t.Error("malformed id accepted")
	}
}

func TestEmptyGroupsIsAnArray(t *testing.T) {
	s := NewSigner(newKey(t))
	v, _ := NewVerifier(s.ID())
	id := caller
	id.Groups = nil
	tok, _ := s.Mint(id, "x.gw.mesh.internal", now)
	if !strings.Contains(decodeBody(t, tok), `"groups":[]`) {
		t.Errorf("groups not an empty array: %s", decodeBody(t, tok))
	}
	if _, err := v.Verify(tok, "x.gw.mesh.internal", now); err != nil {
		t.Fatal(err)
	}
}

func decodeBody(t *testing.T, tok string) string {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(strings.Split(tok, ".")[1])
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
