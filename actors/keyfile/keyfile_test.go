package keyfile

import (
	"bytes"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMintsThenReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	k1, minted, err := Load(path)
	if err != nil || !minted {
		t.Fatalf("first Load: minted=%v err=%v", minted, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 || fi.Size() != ed25519.SeedSize {
		t.Fatalf("key file mode %v size %d", fi.Mode().Perm(), fi.Size())
	}
	k2, minted, err := Load(path)
	if err != nil || minted {
		t.Fatalf("second Load: minted=%v err=%v", minted, err)
	}
	if !bytes.Equal(k1, k2) {
		t.Fatal("reloaded key differs")
	}
}

func TestLoadRefusesWrongSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(path); err == nil {
		t.Fatal("5-byte key accepted")
	}
	if b, _ := os.ReadFile(path); string(b) != "short" {
		t.Fatal("bad key file was overwritten")
	}
}
