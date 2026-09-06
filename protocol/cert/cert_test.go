package cert

import (
	"crypto/ed25519"
	"encoding/hex"
	"strings"
	"testing"

	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// --- canonical JSON vectors (hand-computed) -------------------------

func TestCanonicalBytesVector(t *testing.T) {
	// A member cert with a mix of empty and non-empty caveats. The
	// expected form: top keys sorted aud,can,cav,exp,iat,iss; cav keys
	// sorted delegable,facet,groups,name,target,verbs; empty arrays [].
	c := Cert{
		Iss: ActorID("ed:" + strings.Repeat("ab", 32)),
		Aud: "ed:" + strings.Repeat("cd", 32),
		Can: VerbMember,
		Cav: Caveats{Groups: []string{"admins", "media"}, Name: "laptop"},
		Iat: 100,
		Exp: 200,
	}
	got, err := CanonicalBytes(c)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"aud":"ed:` + strings.Repeat("cd", 32) + `",` +
		`"can":"member",` +
		`"cav":{"delegable":false,"facet":[],"groups":["admins","media"],"name":"laptop","target":[],"verbs":[]},` +
		`"exp":200,"iat":100,` +
		`"iss":"ed:` + strings.Repeat("ab", 32) + `"}`
	if string(got) != want {
		t.Fatalf("canonical mismatch:\n got %s\nwant %s", got, want)
	}
}

func TestCanonicalBytesExcludesSigAndUnknown(t *testing.T) {
	base := Cert{
		Iss: ActorID("ed:" + strings.Repeat("00", 32)),
		Aud: "group:admins",
		Can: VerbInvoke,
		Cav: Caveats{Target: []ActorID{"ed:" + ActorID(strings.Repeat("11", 32))}, Facet: []string{"apid"}},
		Iat: 1, Exp: 2,
	}
	a, _ := CanonicalBytes(base)
	withSig := base
	withSig.Sig = []byte{9, 9, 9}
	withUnknown := base
	withUnknown.Cav.Unknown = true // verifier judgment, not signed
	b, _ := CanonicalBytes(withSig)
	c, _ := CanonicalBytes(withUnknown)
	if string(a) != string(b) || string(a) != string(c) {
		t.Fatalf("sig/unknown leaked into canonical bytes:\n%s\n%s\n%s", a, b, c)
	}
}

func TestCanonicalStringEscaping(t *testing.T) {
	// JCS escapes only " \ and control chars; not < > & (unlike Go's
	// default HTML escaping). A name with these must round-trip.
	c := Cert{
		Iss: ActorID("ed:" + strings.Repeat("00", 32)),
		Aud: "ed:" + strings.Repeat("00", 32),
		Can: VerbMember,
		Cav: Caveats{Name: `a<b>&"q"` + "\t"},
		Iat: 0, Exp: 1,
	}
	got, err := CanonicalBytes(c)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"name":"a<b>&\"q\"\t"`) {
		t.Fatalf("unexpected escaping: %s", got)
	}
}

// --- Ed25519 round trip ---------------------------------------------

func TestEdRoundTrip(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	s := NewEdSigner(priv)
	if err := s.ActorID().Validate(); err != nil {
		t.Fatalf("signer id invalid: %v", err)
	}
	c, err := Sign(Cert{Aud: string(s.ActorID()), Can: VerbMember, Iat: 1, Exp: 2}, s)
	if err != nil {
		t.Fatal(err)
	}
	if c.Iss != s.ActorID() {
		t.Fatalf("Sign did not set iss: %s", c.Iss)
	}
	if err := Verify(c); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// tamper: flip a caveat, sig must fail.
	c.Cav.Name = "tampered"
	if err := Verify(c); err == nil {
		t.Fatal("verify accepted tampered cert")
	}
}

// --- EIP-191 recovery against a known key/signature -----------------

func TestEthRecoveryKnownKey(t *testing.T) {
	// Deterministic key (not secret): private scalar = 1.
	priv := secp.PrivKeyFromBytes(mustHex(t, "0000000000000000000000000000000000000000000000000000000000000001"))
	s := NewEthSigner(priv)
	// Address of privkey 1 is a well-known secp256k1 test vector.
	const wantAddr = "eth:0x7e5f4552091a69125d5dfcb7b8c2659029395bdf"
	if string(s.ActorID()) != wantAddr {
		t.Fatalf("eth actor id = %s, want %s", s.ActorID(), wantAddr)
	}
	msg := []byte("hello EIP-191")
	sig := signPersonalSign(priv, msg)
	got, err := recoverPersonalSign(msg, sig)
	if err != nil {
		t.Fatal(err)
	}
	if "eth:"+got != wantAddr {
		t.Fatalf("recovered %s, want %s", got, wantAddr)
	}
}

func TestEthRoundTripCert(t *testing.T) {
	priv := secp.PrivKeyFromBytes(mustHex(t, "0000000000000000000000000000000000000000000000000000000000000002"))
	s := NewEthSigner(priv)
	c, err := Sign(Cert{
		Aud: "ed:" + strings.Repeat("00", 32),
		Can: VerbSpeakAs,
		Cav: Caveats{Verbs: []string{"member", "invoke"}, Groups: []string{"admins"}},
		Iat: 10, Exp: 20,
	}, s)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(c); err != nil {
		t.Fatalf("verify eth cert: %v", err)
	}
	c.Exp = 99 // tamper
	if err := Verify(c); err == nil {
		t.Fatal("accepted tampered eth cert")
	}
}

// --- strict decode: unknown caveat, unknown verb --------------------

func TestDecodeUnknownCaveatRejects(t *testing.T) {
	valid := `{"iss":"ed:` + strings.Repeat("00", 32) + `","aud":"ed:` + strings.Repeat("00", 32) +
		`","can":"member","cav":{"target":[],"facet":[],"groups":[],"name":"x","delegable":false,"verbs":[]},"iat":1,"exp":2,"sig":""}`
	if _, err := DecodeCert([]byte(valid)); err != nil {
		t.Fatalf("valid cert rejected: %v", err)
	}
	unknownCav := `{"iss":"ed:` + strings.Repeat("00", 32) + `","aud":"ed:` + strings.Repeat("00", 32) +
		`","can":"member","cav":{"rate":5,"target":[],"facet":[],"groups":[],"name":"x","delegable":false,"verbs":[]},"iat":1,"exp":2,"sig":""}`
	if _, err := DecodeCert([]byte(unknownCav)); err == nil {
		t.Fatal("unknown caveat key accepted")
	}
	unknownTop := strings.Replace(valid, `"iat":1`, `"iat":1,"extra":9`, 1)
	if _, err := DecodeCert([]byte(unknownTop)); err == nil {
		t.Fatal("unknown top-level key accepted")
	}
}

func TestDecodeUnknownVerbRejects(t *testing.T) {
	bad := `{"iss":"ed:` + strings.Repeat("00", 32) + `","aud":"ed:` + strings.Repeat("00", 32) +
		`","can":"teleport","cav":{"target":[],"facet":[],"groups":[],"name":"","delegable":false,"verbs":[]},"iat":1,"exp":2,"sig":""}`
	if _, err := DecodeCert([]byte(bad)); err == nil {
		t.Fatal("unknown verb accepted")
	}
}

func TestDecodeEncodeRoundTrip(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	s := NewEdSigner(priv)
	c, _ := Sign(Cert{
		Aud: "group:media", Can: VerbInvoke,
		Cav: Caveats{Target: []ActorID{s.ActorID()}, Facet: []string{"apid"}, Delegable: true},
		Iat: 5, Exp: 6,
	}, s)
	enc, err := Encode(c)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := DecodeCert(enc)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(dec); err != nil {
		t.Fatalf("decoded cert fails verify: %v", err)
	}
}

func TestActorIDValidation(t *testing.T) {
	good := []ActorID{
		ActorID("ed:" + strings.Repeat("ab", 32)),
		ActorID("eth:0x" + strings.Repeat("cd", 20)),
	}
	for _, g := range good {
		if err := g.Validate(); err != nil {
			t.Errorf("%s rejected: %v", g, err)
		}
	}
	bad := []ActorID{
		"ed:" + ActorID(strings.Repeat("ab", 31)),    // too short
		"eth:0x" + ActorID(strings.Repeat("AB", 20)), // uppercase
		"eth:" + ActorID(strings.Repeat("cd", 20)),   // missing 0x
		"foo:bar",
		"",
	}
	for _, b := range bad {
		if err := b.Validate(); err == nil {
			t.Errorf("%s accepted", b)
		}
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
