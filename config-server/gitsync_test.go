package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestGitSyncServesSignedTip runs the whole served-from-git path against
// a throwaway repo: ssh-signed commits via the git binary, go-git's
// file transport, the symlink root, the override and the status line.
// Needs git + ssh-keygen (skipped in the nix sandbox; gitsrc's own
// tests cover verify/materialize/swap without either).
func TestGitSyncServesSignedTip(t *testing.T) {
	for _, bin := range []string{"git", "ssh-keygen"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("no %s", bin)
		}
	}
	tmp := t.TempDir()
	key := filepath.Join(tmp, "key")
	sh(t, tmp, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key)
	pub, _ := os.ReadFile(key + ".pub")
	signers := "owner@test " + string(pub)

	// The remote: talos/ with a machine, signed.
	repo := filepath.Join(tmp, "repo")
	sh(t, tmp, "git", "init", "-q", "-b", "main", repo)
	gitc := func(dir string, args ...string) string {
		return sh(t, dir, append([]string{"git",
			"-c", "user.name=t", "-c", "user.email=owner@test",
			"-c", "gpg.format=ssh", "-c", "user.signingkey=" + key + ".pub",
			"-c", "commit.gpgsign=true"}, args...)...)
	}
	write := func(rel, content string) {
		p := filepath.Join(repo, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
	}
	write("talos/machines/aa/machine.yaml", "v1\n")
	write("talos/allowed-signers", signers)
	write("README.md", "x")
	gitc(repo, "add", "-A")
	gitc(repo, "commit", "-q", "-m", "one")
	v1 := strings.TrimSpace(gitc(repo, "rev-parse", "HEAD"))

	// The hub side: a baked tree and the symlink root over it.
	base := filepath.Join(tmp, "hub")
	baked := filepath.Join(base, "talos-baked")
	os.MkdirAll(filepath.Join(baked, "machines"), 0o755)
	os.WriteFile(filepath.Join(baked, "allowed-signers"), []byte(signers), 0o600)
	root := filepath.Join(base, "talos")
	if err := os.Symlink(baked, root); err != nil {
		t.Fatal(err)
	}

	// A plain directory as root is refused.
	if _, err := newGitSync(baked, repo, "main", "talos", time.Hour, nil); err == nil {
		t.Fatal("plain dir root accepted")
	}

	var decrypted []string
	gs, err := newGitSync(root, repo, "main", "talos", time.Hour, func(dir string) error {
		decrypted = append(decrypted, dir)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	line, warn := gs.statusLine(time.Now())
	if !warn || !strings.Contains(line, "baked") {
		t.Fatalf("before sync: %q warn=%v", line, warn)
	}

	// Only talos/ is served (Subdir), not the repo root.
	gs.syncOnce(ctx, nil)
	if gs.syncer.Current() == nil || gs.syncer.Current().Hash != v1 {
		t.Fatalf("not serving v1: %+v", gs.syncer.Current())
	}
	if got := read(t, filepath.Join(root, "machines/aa/machine.yaml")); got != "v1\n" {
		t.Fatalf("served %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "README.md")); !os.IsNotExist(err) {
		t.Fatal("repo root served")
	}
	if len(decrypted) != 1 || !strings.HasSuffix(decrypted[0], ".tmp") {
		t.Fatalf("decrypt calls %v", decrypted)
	}
	if resolveRoot(root) == root || !strings.Contains(resolveRoot(root), v1) {
		t.Fatalf("resolveRoot %s", resolveRoot(root))
	}
	line, warn = gs.statusLine(time.Now())
	if warn || !strings.Contains(line, "main@"+v1[:12]) || !strings.Contains(line, "owner@test") {
		t.Fatalf("after sync: %q warn=%v", line, warn)
	}

	// An unsigned push: refused, v1 stays, status warns.
	write("talos/machines/aa/machine.yaml", "evil\n")
	gitc(repo, "add", "-A")
	gitc(repo, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "unsigned")
	gs.syncOnce(ctx, nil)
	if got := read(t, filepath.Join(root, "machines/aa/machine.yaml")); got != "v1\n" {
		t.Fatalf("unsigned tip served: %q", got)
	}
	line, warn = gs.statusLine(time.Now())
	if !warn || !strings.Contains(line, "not signed") {
		t.Fatalf("after unsigned: %q warn=%v", line, warn)
	}

	// A signed branch, served by override; the after hook fires.
	gitc(repo, "checkout", "-q", "-b", "exp")
	write("talos/machines/aa/machine.yaml", "exp\n")
	gitc(repo, "add", "-A")
	gitc(repo, "commit", "-q", "-m", "exp")
	if err := gs.setRef("exp"); err != nil {
		t.Fatal(err)
	}
	fired := false
	gs.syncOnce(ctx, func() { fired = true })
	if got := read(t, filepath.Join(root, "machines/aa/machine.yaml")); got != "exp\n" || !fired {
		t.Fatalf("override: %q fired=%v", got, fired)
	}
	line, warn = gs.statusLine(time.Now())
	if !warn || !strings.Contains(line, "OVERRIDE") {
		t.Fatalf("override line: %q warn=%v", line, warn)
	}

	// Back to default (which is now the unsigned tip: refused, exp stays).
	if err := gs.setRef(""); err != nil {
		t.Fatal(err)
	}
	gs.syncOnce(ctx, nil)
	if got := read(t, filepath.Join(root, "machines/aa/machine.yaml")); got != "exp\n" {
		t.Fatalf("after revert to unsigned main: %q", got)
	}
	line, _ = gs.statusLine(time.Now())
	if !strings.Contains(line, "switching to main") {
		t.Fatalf("revert line: %q", line)
	}

	for _, bad := range []string{"-x", "a b", "a..b", "x~1", "r:ef"} {
		if err := gs.setRef(bad); err == nil {
			t.Errorf("setRef(%q) accepted", bad)
		}
	}
}

func sh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return string(out)
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
