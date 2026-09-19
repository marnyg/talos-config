package boottoken

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMintVerify(t *testing.T) {
	master := bytes.Repeat([]byte{7}, 32)
	now := time.Unix(1_700_000_000, 0)
	tok, err := Mint(master, "aa-bb-cc-dd-ee-ff", now)
	if err != nil {
		t.Fatal(err)
	}
	mac, err := Verify(master, tok, now.Add(10*time.Minute))
	if err != nil || mac != "aa-bb-cc-dd-ee-ff" {
		t.Fatalf("verify: %q %v", mac, err)
	}
	// Any process with the master verifies (stateless); a different
	// master does not.
	if _, err := Verify(bytes.Repeat([]byte{8}, 32), tok, now); !errors.Is(err, ErrSignature) {
		t.Fatalf("other master: %v", err)
	}
	// TTL both ways.
	if _, err := Verify(master, tok, now.Add(TTL+time.Second)); !errors.Is(err, ErrExpired) {
		t.Fatalf("after TTL: %v", err)
	}
	if _, err := Verify(master, tok, now.Add(-Skew-time.Second)); !errors.Is(err, ErrExpired) {
		t.Fatalf("before mint: %v", err)
	}
	if _, err := Verify(master, tok, now.Add(-Skew+time.Second)); err != nil {
		t.Fatalf("inside skew: %v", err)
	}
	// Two serves in one second are two tokens (the nonce).
	tok2, _ := Mint(master, "aa-bb-cc-dd-ee-ff", now)
	if tok2 == tok {
		t.Fatal("same token twice")
	}
	// Tampering with the MAC breaks the signature.
	body := strings.Split(tok, ".")
	forged := body[0] + "." + strings.Split(tok2, ".")[1] + "." + body[2]
	if _, err := Verify(master, forged, now); !errors.Is(err, ErrSignature) {
		t.Fatalf("forged: %v", err)
	}
	for _, bad := range []string{"", "bt1.", "x." + body[1] + "." + body[2], "bt1.!!.!!"} {
		if _, err := Verify(master, bad, now); !errors.Is(err, ErrMalformed) && !errors.Is(err, ErrSignature) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestSeen(t *testing.T) {
	master := bytes.Repeat([]byte{7}, 32)
	now := time.Unix(1_700_000_000, 0)
	tok, _ := Mint(master, "aa-bb-cc-dd-ee-ff", now)
	var s Seen
	if err := s.Use(tok, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Use(tok, now.Add(time.Minute)); !errors.Is(err, ErrReplayed) {
		t.Fatalf("replay: %v", err)
	}
	// Pruned once it cannot verify anyway; a fresh Seen (redeploy)
	// never knew it — the accepted residual.
	if err := s.Use(tok, now.Add(TTL+Skew+time.Second)); err != nil {
		t.Fatalf("after prune: %v", err)
	}
	if err := new(Seen).Use(tok, now); err != nil {
		t.Fatalf("fresh process: %v", err)
	}
}
