// Package keyfile is the persisted identity of a long-lived actor
// process (the provisioner, the laptop parent): a 32-byte Ed25519 seed
// in one file, minted on first use. The key is born where it lives and
// never travels (protocol invariant 7); only a process whose id others
// hold chains to persists it — a spawned child never does.
package keyfile

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
)

// Load reads the seed at path, or — when there is no file — mints one
// and writes it 0600. minted reports the second case. A file of the
// wrong size is an error, never overwritten.
func Load(path string) (key ed25519.PrivateKey, minted bool, err error) {
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(b) != ed25519.SeedSize {
			return nil, false, fmt.Errorf("%s: %d bytes, want %d", path, len(b), ed25519.SeedSize)
		}
		return ed25519.NewKeyFromSeed(b), false, nil
	case errors.Is(err, os.ErrNotExist):
		seed := make([]byte, ed25519.SeedSize)
		if _, err := rand.Read(seed); err != nil {
			return nil, false, err
		}
		// O_EXCL: two processes racing on one state dir must not each
		// mint a different identity and both believe theirs.
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, false, err
		}
		if _, err := f.Write(seed); err != nil {
			f.Close()
			return nil, false, err
		}
		if err := f.Close(); err != nil {
			return nil, false, err
		}
		return ed25519.NewKeyFromSeed(seed), true, nil
	default:
		return nil, false, err
	}
}
