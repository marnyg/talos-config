package main

// Round-trips the hand-rolled bech32 in wgderive through the real age
// library: if the derived identity or recipient encoding is wrong in
// any way, these tests fail — a bad encoding cannot ship silently.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/marnyg/talos-config/config-server/masterderive"
)

func testAgeMaster(t *testing.T) []byte {
	t.Helper()
	m, err := masterderive.MasterFromHex("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAgeIdentityParsesAndMatches(t *testing.T) {
	idStr, recipStr := masterderive.AgeIdentity(testAgeMaster(t))

	id, err := age.ParseX25519Identity(idStr)
	if err != nil {
		t.Fatalf("age rejects derived identity: %v", err)
	}
	if got := id.Recipient().String(); got != recipStr {
		t.Fatalf("derived recipient %q != age-computed recipient %q", recipStr, got)
	}
}

func TestAgeEncryptDecryptRoundTrip(t *testing.T) {
	idStr, recipStr := masterderive.AgeIdentity(testAgeMaster(t))
	id, err := age.ParseX25519Identity(idStr)
	if err != nil {
		t.Fatal(err)
	}
	recip, err := age.ParseX25519Recipient(recipStr)
	if err != nil {
		t.Fatalf("age rejects derived recipient: %v", err)
	}

	var ct bytes.Buffer
	w, err := age.Encrypt(&ct, recip)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "cluster secrets"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	r, err := age.Decrypt(&ct, id)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "cluster secrets" {
		t.Fatalf("round trip mangled payload: %q", got)
	}
}

func TestDecryptAgeSecrets(t *testing.T) {
	master := testAgeMaster(t)
	_, recipStr := masterderive.AgeIdentity(master)
	recip, err := age.ParseX25519Recipient(recipStr)
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	dir := filepath.Join(root, "clusters", "homelab")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	encrypt := func(path, content string) {
		t.Helper()
		var ct bytes.Buffer
		w, err := age.Encrypt(&ct, recip)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, content)
		w.Close()
		if err := os.WriteFile(path, ct.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	encrypt(filepath.Join(dir, "secrets.yaml.age"), "secret: yes\n")

	// Pre-existing plaintext must not be overwritten (local dev).
	encrypt(filepath.Join(dir, "sealed-secrets.yaml.age"), "from-age\n")
	if err := os.WriteFile(filepath.Join(dir, "sealed-secrets.yaml"), []byte("pre-existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := decryptAgeSecrets(root, master); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "secrets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "secret: yes\n" {
		t.Fatalf("decrypted content wrong: %q", got)
	}
	pre, _ := os.ReadFile(filepath.Join(dir, "sealed-secrets.yaml"))
	if string(pre) != "pre-existing\n" {
		t.Fatalf("pre-existing plaintext was overwritten: %q", pre)
	}

	// Ciphertext for a different recipient must fail the whole unseal.
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	var ct bytes.Buffer
	w, _ := age.Encrypt(&ct, other.Recipient())
	io.WriteString(w, "not ours")
	w.Close()
	if err := os.WriteFile(filepath.Join(dir, "foreign.yaml.age"), ct.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	err = decryptAgeSecrets(root, master)
	if err == nil || !strings.Contains(err.Error(), "wallet-derived identity") {
		t.Fatalf("foreign ciphertext must fail loudly, got: %v", err)
	}
}
