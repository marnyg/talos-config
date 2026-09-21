package postage

import (
	"context"
	"errors"
	"testing"
)

var pow = PoW{}

func TestPoWRoundTrip(t *testing.T) {
	pre := []byte("preimage")
	req := Require(12)
	tok, err := pow.Solve(context.Background(), req, pre)
	if err != nil {
		t.Fatal(err)
	}
	if err := pow.Check(req, pre, tok); err != nil {
		t.Fatalf("check: %v", err)
	}
	// Bound to the preimage: another envelope's stamp is worthless.
	if err := pow.Check(req, []byte("other"), tok); !errors.Is(err, ErrInsufficient) && err != nil {
		t.Fatalf("other preimage: want insufficient or pass-by-luck, got %v", err)
	}
	// A stiffer requirement is (with overwhelming probability) unmet.
	if err := pow.Check(Require(60), pre, tok); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("60 bits: want ErrInsufficient, got %v", err)
	}
}

func TestPoWFailClosed(t *testing.T) {
	pre := []byte("p")
	cases := []struct {
		req, tok string
		want     error
	}{
		{"fee:1", "00", ErrUnknownScheme},
		{"pow:", "00", ErrMalformed},
		{"pow:0", "00", ErrMalformed},
		{"pow:65", "00", ErrMalformed},
		{"pow:1", "", ErrMissing},
		{"pow:1", "zz", ErrMalformed},
	}
	for _, c := range cases {
		if err := pow.Check(c.req, pre, c.tok); !errors.Is(err, c.want) {
			t.Errorf("Check(%q,%q): got %v want %v", c.req, c.tok, err, c.want)
		}
	}
	if _, err := pow.Solve(context.Background(), "fee:1", pre); !errors.Is(err, ErrUnknownScheme) {
		t.Errorf("Solve unknown scheme: %v", err)
	}
}

func TestPoWSolveHonoursCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := pow.Solve(ctx, Require(64), []byte("p")); !errors.Is(err, context.Canceled) {
		t.Fatalf("want Canceled, got %v", err)
	}
}

func TestDefaultRequireIsInVocabulary(t *testing.T) {
	bits, err := ParsePoW(DefaultRequire)
	if err != nil || bits != DefaultPoWBits {
		t.Fatalf("DefaultRequire %q: bits %d err %v", DefaultRequire, bits, err)
	}
}
