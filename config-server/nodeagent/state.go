package nodeagent

// The agent's state directory (/var/lib/p0agent on a node: EPHEMERAL —
// survives reboot and upgrade, not a wipe; exactly a member's
// durability). Invariant 2, actor-owned state: the KEY is the one thing
// that is state — possession is the credential. Everything else here is
// the member's own certs (the grant is the record: the grantee stores
// and presents them) and safe-to-lose caches (the last bundle, the hub's
// location, the clock mark): losing any of them costs a beat, never a
// grant.

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/marnyg/talos-config/config-server/issuer"
	"github.com/marnyg/talos-config/protocol/cert"
)

// State-dir file names.
const (
	// KeyFile holds the 32-byte Ed25519 seed — the NodeId. Same file
	// the P0.3 probe minted, so cp1 keeps the id it has had since
	// 2026-09-15.
	KeyFile = "key"
	// KitFile is the member's Kit (issuer.EncodeKit): member cert, beat
	// grant, and the speak-as that resolves their issuer.
	KitFile = "kit.json"
	// BundleFile is the last #bundle reply (issuer.EncodeBundle): grants,
	// blocklist, name map, current speak-as. The hub's witness cache is
	// empty after a deploy until members beat, so the member keeps its
	// own last copy (decision 2fc).
	BundleFile = "bundle.json"
	// HubFile is the hub's last known speak-as + reach-me-at (HubRecord),
	// so a beat needs no WAN fetch while the hub has not rotated.
	HubFile = "hub.json"
	// MarkFile is the ADR-0019 low-water mark, decimal Unix seconds.
	MarkFile = "mark"
)

// State is the directory and the codecs over it.
type State struct{ Dir string }

func (s State) path(name string) string { return filepath.Join(s.Dir, name) }

// writeFile writes atomically (tmp + rename), 0600.
func (s State) writeFile(name string, b []byte) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	tmp := s.path(name) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(name))
}

// Key loads the node key, minting and persisting one on first run.
func (s State) Key() (ed25519.PrivateKey, bool, error) {
	b, err := os.ReadFile(s.path(KeyFile))
	switch {
	case err == nil:
		if len(b) != ed25519.SeedSize {
			return nil, false, fmt.Errorf("%s: %d bytes, want %d", s.path(KeyFile), len(b), ed25519.SeedSize)
		}
		return ed25519.NewKeyFromSeed(b), false, nil
	case errors.Is(err, os.ErrNotExist):
		seed := make([]byte, ed25519.SeedSize)
		if _, err := rand.Read(seed); err != nil {
			return nil, false, err
		}
		if err := s.writeFile(KeyFile, seed); err != nil {
			return nil, false, err
		}
		return ed25519.NewKeyFromSeed(seed), true, nil
	default:
		return nil, false, err
	}
}

// Kit loads the persisted Kit; ok=false when there is none yet.
func (s State) Kit() (issuer.Kit, bool, error) {
	b, err := os.ReadFile(s.path(KitFile))
	if errors.Is(err, os.ErrNotExist) {
		return issuer.Kit{}, false, nil
	}
	if err != nil {
		return issuer.Kit{}, false, err
	}
	k, err := issuer.DecodeKit(b)
	if err != nil {
		return issuer.Kit{}, false, fmt.Errorf("%s: %w", s.path(KitFile), err)
	}
	return k, true, nil
}

// SaveKit persists k.
func (s State) SaveKit(k issuer.Kit) error {
	b, err := issuer.EncodeKit(k)
	if err != nil {
		return err
	}
	return s.writeFile(KitFile, b)
}

// Bundle loads the last bundle; ok=false when there is none.
func (s State) Bundle() (issuer.Bundle, bool, error) {
	b, err := os.ReadFile(s.path(BundleFile))
	if errors.Is(err, os.ErrNotExist) {
		return issuer.Bundle{}, false, nil
	}
	if err != nil {
		return issuer.Bundle{}, false, err
	}
	bd, err := issuer.DecodeBundle(b)
	if err != nil {
		return issuer.Bundle{}, false, fmt.Errorf("%s: %w", s.path(BundleFile), err)
	}
	return bd, true, nil
}

// SaveBundle persists b.
func (s State) SaveBundle(b issuer.Bundle) error {
	raw, err := issuer.EncodeBundle(b)
	if err != nil {
		return err
	}
	return s.writeFile(BundleFile, raw)
}

// HubRecord is what the agent knows about the hub: the wallet's
// speak-as to the current hubkey and that hubkey's reach-me-at. Both
// are verified offline; the WAN fetch (/.well-known) is only the hint
// channel.
type HubRecord struct {
	SpeakAs   cert.Cert
	ReachMeAt cert.Cert
}

// ID is the hubkey the record names.
func (h HubRecord) ID() cert.ActorID { return cert.ActorID(h.SpeakAs.Aud) }

type wireHub struct {
	SpeakAs   json.RawMessage `json:"speak_as"`
	ReachMeAt json.RawMessage `json:"reach_me_at"`
}

// Hub loads the cached hub record; ok=false when there is none.
func (s State) Hub() (HubRecord, bool, error) {
	b, err := os.ReadFile(s.path(HubFile))
	if errors.Is(err, os.ErrNotExist) {
		return HubRecord{}, false, nil
	}
	if err != nil {
		return HubRecord{}, false, err
	}
	var w wireHub
	if err := json.Unmarshal(b, &w); err != nil {
		return HubRecord{}, false, fmt.Errorf("%s: %w", s.path(HubFile), err)
	}
	var h HubRecord
	if h.SpeakAs, err = cert.DecodeCert(w.SpeakAs); err != nil {
		return HubRecord{}, false, fmt.Errorf("%s: speak-as: %w", s.path(HubFile), err)
	}
	if h.ReachMeAt, err = cert.DecodeCert(w.ReachMeAt); err != nil {
		return HubRecord{}, false, fmt.Errorf("%s: reach-me-at: %w", s.path(HubFile), err)
	}
	return h, true, nil
}

// SaveHub persists h.
func (s State) SaveHub(h HubRecord) error {
	sa, err := cert.Encode(h.SpeakAs)
	if err != nil {
		return err
	}
	loc, err := cert.Encode(h.ReachMeAt)
	if err != nil {
		return err
	}
	b, err := json.Marshal(wireHub{SpeakAs: sa, ReachMeAt: loc})
	if err != nil {
		return err
	}
	return s.writeFile(HubFile, b)
}

// Mark loads the persisted low-water mark (0 when absent or unreadable
// — it is a cache).
func (s State) Mark() int64 {
	b, err := os.ReadFile(s.path(MarkFile))
	if err != nil {
		return 0
	}
	lw, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return lw
}

// SaveMark persists lw.
func (s State) SaveMark(lw int64) error {
	return s.writeFile(MarkFile, []byte(strconv.FormatInt(lw, 10)+"\n"))
}
