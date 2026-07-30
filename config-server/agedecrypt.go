package main

// Unseal-time secrets decryption: the image ships only .age
// ciphertext; the wallet-derived age identity (masterderive.AgeIdentity,
// same master as everything else) decrypts the cluster secrets into
// the (tmpfs) tree when the admin unseals. Until then not even the
// server process can read them — strictly better than the old
// entrypoint flow, which held plaintext in tmpfs from boot with a
// dedicated AGE_KEY fly secret.

import (
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"

	"github.com/marnyg/talos-config/config-server/masterderive"
)

// decryptAgeSecrets walks root's clusters/ tree and decrypts every
// *.age beside its ciphertext. Files whose plaintext already exists
// are skipped (local dev: the devshell decrypts with the ssh key and
// the dev master would derive the wrong identity). Any actual
// decryption failure aborts — an unseal that cannot produce the
// secrets must fail loudly, not serve broken configs.
func decryptAgeSecrets(root string, master []byte) error {
	idStr, _ := masterderive.AgeIdentity(master)
	id, err := age.ParseX25519Identity(idStr)
	if err != nil {
		return fmt.Errorf("parsing derived age identity: %w", err)
	}

	clusters := filepath.Join(root, "clusters")
	if _, err := os.Stat(clusters); os.IsNotExist(err) {
		return nil
	}
	return filepath.WalkDir(clusters, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".age") {
			return err
		}
		out := strings.TrimSuffix(path, ".age")
		if _, err := os.Stat(out); err == nil {
			log.Printf("age: %s already present, not overwriting", rel(root, out))
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		r, err := age.Decrypt(in, id)
		if err != nil {
			return fmt.Errorf("decrypting %s with the wallet-derived identity: %w (is it encrypted to talos/age-recipient.txt?)", rel(root, path), err)
		}
		plain, err := io.ReadAll(r)
		if err != nil {
			return fmt.Errorf("reading %s: %w", rel(root, path), err)
		}
		if err := os.WriteFile(out, plain, 0o600); err != nil {
			return err
		}
		log.Printf("age: decrypted %s", rel(root, out))
		return nil
	})
}

func rel(root, path string) string {
	if r, err := filepath.Rel(root, path); err == nil {
		return r
	}
	return path
}
