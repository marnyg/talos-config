package policy

// Pins from git: the durable copy of the hub's commitments to the
// identities it resolves root keys for (protocol ADR-0010/0011, d0o0).
// A cert.Pin is volatile by design — a restart that loses Held()
// revives a dead identity at epoch 0 and re-admits a rotated-away root
// — and the equivocation record is NOT safe-to-lose by ADR-0019's test,
// so talos/pins/<address>.json holds it, signed by the owner's ssh key
// like every other talos/ file (ADR-0010 "N=1 naming": the ssh key is
// the pin AUTHORITY for the hub's view, never a guardian). The hub
// rebuilds its pins from this directory on every git tip; members get
// the rotations through #bundle and fold them into their own pins.
//
// File shape (one identity per file; the name is a convention, the
// address inside is what counts):
//
//	{
//	  "address":   "eth:0x…",
//	  "policy0":   {"root": "eth:0x…", "guardians": ["eth:0x…"], "k": 1},
//	  "rotations": [[<rotate-root cert wire JSON> …] …]
//	}
//
// policy0 is the epoch-0 policy the pin commits to; rotations are the
// verified transitions in the order held, each one rotation's k
// guardian certs. Nothing here is trusted on read: FoldRotations
// re-verifies every cert (cert.AssembleRotation + Pin.Hold) exactly as
// a member does with a #bundle. A missing directory is "no pins from
// git" (the hub pins each -admin-address trivially); a malformed file
// is an error, never a silently skipped identity.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/marnyg/talos-config/protocol/cert"
)

// PinsDir is the directory under talos/ holding one pin file per
// identity.
const PinsDir = "pins"

// PinRecord is one pin file, decoded and shape-checked. Rotations are
// certs as the file carried them; build the pin with FoldRotations.
type PinRecord struct {
	Address   cert.ActorID
	Policy0   cert.Policy
	Rotations [][]cert.Cert
}

type wirePin struct {
	Address   string              `json:"address"`
	Policy0   wirePolicy          `json:"policy0"`
	Rotations [][]json.RawMessage `json:"rotations"`
}

// wirePolicy is cert.Policy's JSON shape — the same keys a rotate-root
// cert's cav.policy uses.
type wirePolicy struct {
	Root      string   `json:"root"`
	Guardians []string `json:"guardians"`
	K         int      `json:"k"`
}

// LoadPins reads every <root>/pins/*.json. A missing directory yields
// nil; a malformed file, or a file whose name does not match its
// address (which also makes two files for one identity impossible), is
// an error. Sorted by address so the hub holds pins in a stable order.
func LoadPins(root string) ([]PinRecord, error) {
	dir := filepath.Join(root, PinsDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", PinsDir, err)
	}
	var out []PinRecord
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("reading %s/%s: %w", PinsDir, name, err)
		}
		rec, err := ParsePin(raw)
		if err != nil {
			return nil, fmt.Errorf("%s/%s: %w", PinsDir, name, err)
		}
		if want := PinFileName(rec.Address); name != want {
			return nil, fmt.Errorf("%s/%s: names %s; expected file name %s", PinsDir, name, rec.Address, want)
		}
		out = append(out, rec)
	}
	slices.SortFunc(out, func(a, b PinRecord) int { return strings.Compare(string(a.Address), string(b.Address)) })
	return out, nil
}

// PinFileName is the file an identity's pin lives in: the address with
// the scheme's colon dropped (eth:0xabc… → eth-0xabc….json), so the name
// is portable and greppable for the address.
func PinFileName(address cert.ActorID) string {
	return strings.Replace(string(address), ":", "-", 1) + ".json"
}

// ParsePin is LoadPins over one file's bytes. Strict: an unknown key
// fails loudly (a typoed "guardian" would otherwise pin an identity to
// a policy nobody wrote), the address and policy0 validate, and every
// rotation cert decodes as rotate-root for the address. Verification
// of the rotations is FoldRotations's.
func ParsePin(raw []byte) (PinRecord, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var w wirePin
	if err := dec.Decode(&w); err != nil {
		return PinRecord{}, fmt.Errorf("parsing: %w", err)
	}
	if dec.More() {
		return PinRecord{}, fmt.Errorf("parsing: trailing data")
	}
	rec := PinRecord{Address: cert.ActorID(w.Address)}
	if err := rec.Address.Validate(); err != nil {
		return PinRecord{}, fmt.Errorf("address: %w", err)
	}
	rec.Policy0 = cert.Policy{Root: cert.ActorID(w.Policy0.Root), K: w.Policy0.K}
	for _, g := range w.Policy0.Guardians {
		rec.Policy0.Guardians = append(rec.Policy0.Guardians, cert.ActorID(g))
	}
	if err := rec.Policy0.Validate(); err != nil {
		return PinRecord{}, fmt.Errorf("policy0: %w", err)
	}
	for i, r := range w.Rotations {
		if len(r) == 0 {
			return PinRecord{}, fmt.Errorf("rotations[%d]: no certs", i)
		}
		certs := make([]cert.Cert, 0, len(r))
		for j, rc := range r {
			c, err := cert.DecodeCert(rc)
			if err != nil {
				return PinRecord{}, fmt.Errorf("rotations[%d][%d]: %w", i, j, err)
			}
			if c.Can != cert.VerbRotateRoot {
				return PinRecord{}, fmt.Errorf("rotations[%d][%d]: can %q is not rotate-root", i, j, c.Can)
			}
			if c.Aud != string(rec.Address) {
				return PinRecord{}, fmt.Errorf("rotations[%d][%d]: aud %q is not %s", i, j, c.Aud, rec.Address)
			}
			certs = append(certs, c)
		}
		rec.Rotations = append(rec.Rotations, certs)
	}
	return rec, nil
}

// EncodePin renders a pin as its file — what an operator commits after
// a rotation (policy0 and the certs as held). The inverse of ParsePin.
func EncodePin(address cert.ActorID, policy0 cert.Policy, rotations [][]cert.Cert) ([]byte, error) {
	w := wirePin{Address: string(address), Policy0: policyToWire(policy0), Rotations: [][]json.RawMessage{}}
	for _, r := range rotations {
		certs := make([]json.RawMessage, 0, len(r))
		for _, c := range r {
			raw, err := cert.Encode(c)
			if err != nil {
				return nil, err
			}
			certs = append(certs, raw)
		}
		w.Rotations = append(w.Rotations, certs)
	}
	return json.MarshalIndent(w, "", "  ")
}

func policyToWire(p cert.Policy) wirePolicy {
	g := make([]string, 0, len(p.Guardians))
	for _, x := range p.Guardians {
		g = append(g, string(x))
	}
	return wirePolicy{Root: string(p.Root), Guardians: g, K: p.K}
}

// FoldRotations returns a FRESH pin equal to pin with every rotation
// that names pin's address folded in — the one fold both sides of the
// beat run: the hub over its git copy, a member over a #bundle. Each
// rotation is re-verified against THIS pin (cert.AssembleRotation
// checks the k guardian signatures and the body; Pin.Hold re-checks
// and folds). A new value, never a mutation: an installed pin is read
// concurrently by every Authorize and cert.Pin is not safe for
// concurrent use — callers HoldPins the result. Rotations for other
// addresses are skipped; a rotation whose prev names no policy this
// pin knows (the epoch-0 policy, the current view, or any held
// rotation's next) cannot be assembled and is reported; an
// equivocation is reported and the pin is dead from then on (nothing
// under that identity verifies — the owner must re-pin out of band).
// now bounds assembly only (the certs' exp), never validity.
func FoldRotations(pin *cert.Pin, rotations [][]cert.Cert, now int64, report func(error)) *cert.Pin {
	out := cert.NewPin(pin.Address, pin.Policy0)
	for _, r := range pin.Held() {
		if err := out.Hold(r); err != nil && !errors.Is(err, cert.ErrNotRooted) && !errors.Is(err, cert.ErrEquivocation) {
			report(fmt.Errorf("pins: re-holding rotation epoch %d: %w", r.Epoch, err))
		}
	}
	for i, certs := range rotations {
		if len(certs) == 0 || certs[0].Aud != string(out.Address) {
			continue
		}
		head := certs[0]
		prev, ok := knownPolicy(out, head.Cav.Prev)
		if !ok {
			report(fmt.Errorf("pins: rotation %d (epoch %d): prev %.8s names no policy this pin knows", i, head.Cav.Epoch, head.Cav.Prev))
			continue
		}
		rot, err := cert.AssembleRotation(out.Address, prev, head.Cav.Epoch, certs, now)
		if err != nil {
			report(fmt.Errorf("pins: rotation %d (epoch %d): %w", i, head.Cav.Epoch, err))
			continue
		}
		if err := out.Hold(rot); err != nil {
			report(fmt.Errorf("pins: rotation %d (epoch %d): %w", i, head.Cav.Epoch, err))
		}
	}
	return out
}

// knownPolicy finds the policy with the given hash among those the pin
// can root a transition from: epoch 0, the current view, and every
// held rotation's next (an out-of-order arrival chains later).
func knownPolicy(pin *cert.Pin, hash string) (cert.Policy, bool) {
	cands := []cert.Policy{pin.Policy0, pin.View().Policy}
	for _, r := range pin.Held() {
		cands = append(cands, r.Next)
	}
	for _, p := range cands {
		if p.Hash() == hash {
			return p, true
		}
	}
	return cert.Policy{}, false
}
