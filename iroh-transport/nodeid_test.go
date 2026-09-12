package irohtransport

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/marnyg/talos-config/iroh-go/iroh"
	"github.com/marnyg/talos-config/protocol/cert"
)

func TestActorIDEndpointIdRoundTrip(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	want := cert.NewEdSigner(priv).ActorID()

	// pub → ActorID is the cert package's own derivation.
	got, err := ActorIDFromPublicKey(pub)
	if err != nil || got != want {
		t.Fatalf("ActorIDFromPublicKey = %s %v, want %s", got, err, want)
	}
	// ActorID → EndpointId → ActorID is the identity.
	eid, err := EndpointIDOf(want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(eid.ToBytes(), pub) {
		t.Fatalf("EndpointId bytes %x != pub %x", eid.ToBytes(), pub)
	}
	back, err := ActorIDOf(eid)
	if err != nil || back != want {
		t.Fatalf("ActorIDOf = %s %v, want %s", back, err, want)
	}
	// iroh's own secret-key path derives the same id from the seed.
	sk, err := iroh.SecretKeyFromBytes(priv.Seed())
	if err != nil {
		t.Fatal(err)
	}
	if !sk.Public().Eq(eid) {
		t.Fatalf("iroh secret key public %s != %s", sk.Public(), eid)
	}
	if hex.EncodeToString(sk.Public().ToBytes()) != string(want[len("ed:"):]) {
		t.Fatal("iroh public key hex != actor id hex")
	}
}

func TestEndpointIDOfRejectsNonEd(t *testing.T) {
	cases := map[string]error{
		"eth:0x1234567890abcdef1234567890abcdef12345678": ErrNotEdActor,
		"ed:" + "zz" + "00000000000000000000000000000000000000000000000000000000000000": cert.ErrBadActorID,
		"ed:00":                cert.ErrBadActorID,
		"sol:whatever":         cert.ErrUnknownScheme,
		"ed:" + upperHex(32):   cert.ErrBadActorID, // uppercase hex is not canonical
		"ed:" + hex.EncodeToString(bytes.Repeat([]byte{0xff}, 32)): nil, // syntactically fine; on-curve-ness is iroh's call
	}
	for id, want := range cases {
		_, err := EndpointIDOf(cert.ActorID(id))
		switch {
		case want == nil:
			// Our layer passes it through; whatever iroh says must not be
			// dressed up as one of our syntax errors.
			if errors.Is(err, ErrNotEdActor) || errors.Is(err, cert.ErrBadActorID) {
				t.Errorf("%s: err = %v", id, err)
			}
		case !errors.Is(err, want):
			t.Errorf("%s: err = %v, want %v", id, err, want)
		}
	}
	if _, err := ActorIDFromPublicKey(make([]byte, 31)); !errors.Is(err, cert.ErrBadActorID) {
		t.Errorf("31-byte key: %v", err)
	}
	if _, err := ActorIDOf(nil); !errors.Is(err, cert.ErrBadActorID) {
		t.Errorf("nil id: %v", err)
	}
}

func upperHex(n int) string {
	return string(bytes.ToUpper([]byte(hex.EncodeToString(bytes.Repeat([]byte{0xab}, n)))))
}
