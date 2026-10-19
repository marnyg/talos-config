package policy

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/marnyg/talos-config/protocol/cert"
)

func ethKey(t *testing.T, seed byte) (*secp256k1.PrivateKey, cert.ActorID) {
	t.Helper()
	b := make([]byte, 32)
	b[31] = seed
	priv := secp256k1.PrivKeyFromBytes(b)
	return priv, cert.NewEthSigner(priv).ActorID()
}

func trivial(a cert.ActorID) cert.Policy {
	return cert.Policy{Root: a, Guardians: []cert.ActorID{a}, K: 1}
}

// rotateCert is one guardian's rotate-root cert for address, prev → next
// at epoch.
func rotateCert(t *testing.T, address cert.ActorID, prev, next cert.Policy, epoch int64, by *secp256k1.PrivateKey) cert.Cert {
	t.Helper()
	c, err := cert.Sign(cert.Cert{Aud: string(address), Can: cert.VerbRotateRoot,
		Cav: cert.Caveats{Epoch: epoch, Prev: prev.Hash(), Policy: &next}, Iat: 1, Exp: 1_000_000}, cert.NewEthSigner(by))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// The shipped pins directory loads: every file is named for its
// address, and every pin folds to a live view (a dead identity on
// record would be a deliberate commit, but the test says so loudly).
func TestLoadShippedPins(t *testing.T) {
	recs, err := LoadPins(filepath.Join(repoRoot(t), "talos"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		p := FoldRotations(cert.NewPin(r.Address, r.Policy0), r.Rotations, 1, func(err error) { t.Errorf("%s: %v", r.Address, err) })
		if p.View().Dead {
			t.Errorf("%s: shipped pin is dead", r.Address)
		}
	}
}

// A pin file round-trips through EncodePin/ParsePin; LoadPins over a
// directory folds back to the same view, and the fold re-verifies the
// certs rather than trusting the file: a tampered successor policy is
// reported and not held.
func TestPinsRoundTrip(t *testing.T) {
	oldKey, old := ethKey(t, 1)
	_, next := ethKey(t, 2)
	p0, p1 := trivial(old), trivial(next)
	rot := []cert.Cert{rotateCert(t, old, p0, p1, 1, oldKey)}

	raw, err := EncodePin(old, p0, [][]cert.Cert{rot})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := ParsePin(raw)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Address != old || rec.Policy0.Hash() != p0.Hash() || len(rec.Rotations) != 1 {
		t.Fatalf("parsed %+v", rec)
	}

	root := t.TempDir()
	dir := filepath.Join(root, PinsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, PinFileName(old)), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("not a pin"), 0o644); err != nil {
		t.Fatal(err)
	}
	recs, err := LoadPins(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Address != old {
		t.Fatalf("loaded %+v", recs)
	}
	var reported []error
	pin := FoldRotations(cert.NewPin(recs[0].Address, recs[0].Policy0), recs[0].Rotations, 1, func(err error) { reported = append(reported, err) })
	if v := pin.View(); len(reported) != 0 || v.Epoch != 1 || v.Policy.Root != next || !pin.Admits(next) || pin.Admits(old) {
		t.Fatalf("view %+v reported %v", pin.View(), reported)
	}

	// Tampered: the file claims a successor the guardian never signed.
	forged := rot[0]
	forged.Cav.Policy = &cert.Policy{Root: old, Guardians: []cert.ActorID{old, next}, K: 2}
	tampered := FoldRotations(cert.NewPin(old, p0), [][]cert.Cert{{forged}}, 1, func(err error) { reported = append(reported, err) })
	if len(reported) != 1 || !errors.Is(reported[0], cert.ErrSigMismatch) || len(tampered.Held()) != 0 || !tampered.Admits(old) {
		t.Fatalf("tampered rotation: reported %v, held %d", reported, len(tampered.Held()))
	}

	// Missing directory: no pins, no error.
	if recs, err := LoadPins(t.TempDir()); err != nil || recs != nil {
		t.Fatalf("missing dir: %v %v", recs, err)
	}
}

// Strictness: a typoed key, a bad address, a policy with k = 0, a
// non-rotate-root cert, a cert for another address and a misnamed file
// are each an error.
func TestPinsReject(t *testing.T) {
	oldKey, old := ethKey(t, 1)
	_, next := ethKey(t, 2)
	p0 := trivial(old)
	good, err := EncodePin(old, p0, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{
		"unknown key":    `{"address":"` + string(old) + `","policy0":{"root":"` + string(old) + `","guardians":["` + string(old) + `"],"k":1},"rotation":[]}`,
		"bad address":    `{"address":"nope","policy0":{"root":"` + string(old) + `","guardians":["` + string(old) + `"],"k":1},"rotations":[]}`,
		"k zero":         `{"address":"` + string(old) + `","policy0":{"root":"` + string(old) + `","guardians":["` + string(old) + `"],"k":0},"rotations":[]}`,
		"empty rotation": `{"address":"` + string(old) + `","policy0":{"root":"` + string(old) + `","guardians":["` + string(old) + `"],"k":1},"rotations":[[]]}`,
	} {
		if _, err := ParsePin([]byte(raw)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// A non-rotation verb, and a rotation for another address.
	invoke, err := cert.Sign(cert.Cert{Aud: string(old), Can: cert.VerbInvoke, Cav: cert.Caveats{Target: []cert.ActorID{old}, Facet: []string{"x"}}, Iat: 1, Exp: 2}, cert.NewEthSigner(oldKey))
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := EncodePin(old, p0, [][]cert.Cert{{invoke}}); err == nil {
		if _, err := ParsePin(raw); err == nil {
			t.Error("invoke cert accepted as a rotation")
		}
	}
	other := rotateCert(t, next, trivial(next), p0, 1, oldKey)
	if raw, err := EncodePin(old, p0, [][]cert.Cert{{other}}); err == nil {
		if _, err := ParsePin(raw); err == nil {
			t.Error("rotation for another address accepted")
		}
	}

	root := t.TempDir()
	dir := filepath.Join(root, PinsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "owner.json"), good, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPins(root); err == nil {
		t.Error("misnamed file accepted")
	}
	if err := os.Rename(filepath.Join(dir, "owner.json"), filepath.Join(dir, PinFileName(old))); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPins(root); err != nil {
		t.Fatalf("well-named file: %v", err)
	}
}
