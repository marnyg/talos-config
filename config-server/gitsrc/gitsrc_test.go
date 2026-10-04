package gitsrc

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/hiddeco/sshsig"
	"golang.org/x/crypto/ssh"
)

// --- fixtures: an in-memory repo with SSHSIG-signed commits, no git binary ---

func newKey(t *testing.T) (ssh.Signer, ssh.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	p, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return s, p
}

func signersFor(principal string, pub ssh.PublicKey) []Signer {
	return []Signer{{Principal: principal, Key: pub}}
}

// repo is a storer plus named heads; it is also the test Fetcher.
type repo struct {
	st    storer.EncodedObjectStorer
	heads map[string]plumbing.Hash
}

func newRepo() *repo {
	return &repo{st: memory.NewStorage(), heads: map[string]plumbing.Hash{}}
}

func (r *repo) Head(_ context.Context, ref string) (plumbing.Hash, error) {
	h, ok := r.heads[ref]
	if !ok {
		return plumbing.ZeroHash, errors.New("no such ref")
	}
	return h, nil
}

func (r *repo) Commit(_ context.Context, ref string) (*object.Commit, error) {
	h, err := r.Head(context.Background(), ref)
	if err != nil {
		return nil, err
	}
	return object.GetCommit(r.st, h)
}

func (r *repo) blob(t *testing.T, content string) plumbing.Hash {
	t.Helper()
	o := r.st.NewEncodedObject()
	o.SetType(plumbing.BlobObject)
	w, _ := o.Writer()
	w.Write([]byte(content))
	w.Close()
	h, err := r.st.SetEncodedObject(o)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// tree builds nested trees from "a/b/c" → content. A content starting
// with "->" is a symlink.
func (r *repo) tree(t *testing.T, files map[string]string) plumbing.Hash {
	t.Helper()
	type node struct {
		files map[string]string
		dirs  map[string]map[string]string
	}
	n := node{files: map[string]string{}, dirs: map[string]map[string]string{}}
	for p, c := range files {
		if i := strings.IndexByte(p, '/'); i >= 0 {
			d := p[:i]
			if n.dirs[d] == nil {
				n.dirs[d] = map[string]string{}
			}
			n.dirs[d][p[i+1:]] = c
		} else {
			n.files[p] = c
		}
	}
	var entries []object.TreeEntry
	for name, c := range n.files {
		mode := filemode.Regular
		if strings.HasPrefix(c, "->") {
			mode = filemode.Symlink
			c = c[2:]
		}
		entries = append(entries, object.TreeEntry{Name: name, Mode: mode, Hash: r.blob(t, c)})
	}
	for name, sub := range n.dirs {
		entries = append(entries, object.TreeEntry{Name: name, Mode: filemode.Dir, Hash: r.tree(t, sub)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	tr := &object.Tree{Entries: entries}
	o := r.st.NewEncodedObject()
	if err := tr.Encode(o); err != nil {
		t.Fatal(err)
	}
	h, err := r.st.SetEncodedObject(o)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// commit writes a commit for files on ref; signer nil = unsigned.
func (r *repo) commit(t *testing.T, ref string, files map[string]string, signer ssh.Signer) *object.Commit {
	t.Helper()
	sig := object.Signature{Name: "t", Email: "t@example", When: time.Unix(1700000000, 0).UTC()}
	c := &object.Commit{Author: sig, Committer: sig, Message: "m\n", TreeHash: r.tree(t, files)}
	if h, ok := r.heads[ref]; ok {
		c.ParentHashes = []plumbing.Hash{h}
	}
	if signer != nil {
		o := &plumbing.MemoryObject{}
		if err := c.EncodeWithoutSignature(o); err != nil {
			t.Fatal(err)
		}
		rd, _ := o.Reader()
		var msg bytes.Buffer
		msg.ReadFrom(rd)
		s, err := sshsig.Sign(&msg, signer, sshsig.HashSHA512, Namespace)
		if err != nil {
			t.Fatal(err)
		}
		c.PGPSignature = string(sshsig.Armor(s))
	}
	o := r.st.NewEncodedObject()
	if err := c.Encode(o); err != nil {
		t.Fatal(err)
	}
	h, err := r.st.SetEncodedObject(o)
	if err != nil {
		t.Fatal(err)
	}
	r.heads[ref] = h
	got, err := object.GetCommit(r.st, h)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

var baseFiles = map[string]string{
	"machines/aa-bb/machine.yaml": "role: worker\n",
	"mesh-policy-v3.yaml":         "node: {}\n",
	"age-recipient.txt":           "age1x\n",
	"README.md":                   "not served\n",
}

// --- ParseSigners ---

func TestParseSigners(t *testing.T) {
	_, pub := newKey(t)
	line := string(ssh.MarshalAuthorizedKey(pub))
	in := "# comment\n\nalice@x " + line +
		"bob@x,carol@x namespaces=\"git\",valid-after=\"20240101\" " + line
	got, err := ParseSigners([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	var ps []string
	for _, s := range got {
		ps = append(ps, s.Principal)
		if !bytes.Equal(s.Key.Marshal(), pub.Marshal()) {
			t.Fatal("key mismatch")
		}
	}
	if strings.Join(ps, ",") != "alice@x,bob@x,carol@x" {
		t.Fatalf("principals %v", ps)
	}
	for _, bad := range []string{"", "# only\n", "alice@x ssh-ed25519\n", "alice@x ssh-ed25519 notbase64!\n"} {
		if _, err := ParseSigners([]byte(bad)); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

// --- Verify ---

func TestVerify(t *testing.T) {
	owner, ownerPub := newKey(t)
	other, _ := newKey(t)
	signers := signersFor("owner@x", ownerPub)
	r := newRepo()

	c := r.commit(t, "main", baseFiles, owner)
	if p, err := Verify(c, signers); err != nil || p != "owner@x" {
		t.Fatalf("owner-signed: %q %v", p, err)
	}

	c = r.commit(t, "main", baseFiles, nil)
	if _, err := Verify(c, signers); !errors.Is(err, ErrUnsigned) {
		t.Fatalf("unsigned: %v", err)
	}

	c = r.commit(t, "main", baseFiles, other)
	if _, err := Verify(c, signers); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("other key: %v", err)
	}

	// Tampered: a signed commit whose message was edited after signing.
	c = r.commit(t, "main", baseFiles, owner)
	c.Message = "edited\n"
	if _, err := Verify(c, signers); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("tampered: %v", err)
	}

	// Not an SSHSIG at all (a PGP-looking header).
	c = r.commit(t, "main", baseFiles, owner)
	c.PGPSignature = "-----BEGIN PGP SIGNATURE-----\nAAAA\n-----END PGP SIGNATURE-----\n"
	if _, err := Verify(c, signers); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("pgp: %v", err)
	}
}

// --- Sync ---

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSync(t *testing.T) {
	owner, ownerPub := newKey(t)
	r := newRepo()
	base := t.TempDir()
	s := &Syncer{Fetch: r, Signers: signersFor("owner@x", ownerPub), Base: base, Link: "talos"}
	link := filepath.Join(base, "talos")
	ctx := context.Background()

	// Unsigned first tip: nothing served.
	r.commit(t, "main", baseFiles, nil)
	if _, changed, err := s.Sync(ctx, "main"); err == nil || changed {
		t.Fatalf("unsigned first tip: changed=%v err=%v", changed, err)
	}
	if _, err := os.Lstat(link); err == nil {
		t.Fatal("link exists after refused tip")
	}

	// Signed tip: materialized, linked, README not served.
	c1 := r.commit(t, "main", baseFiles, owner)
	tip, changed, err := s.Sync(ctx, "main")
	if err != nil || !changed {
		t.Fatalf("first signed: changed=%v err=%v", changed, err)
	}
	if tip.Hash != c1.Hash.String() || tip.Signer != "owner@x" || tip.Ref != "main" {
		t.Fatalf("tip %+v", tip)
	}
	if got := readFile(t, filepath.Join(link, "machines/aa-bb/machine.yaml")); got != "role: worker\n" {
		t.Fatalf("machine.yaml %q", got)
	}
	if _, err := os.Stat(filepath.Join(link, "README.md")); !os.IsNotExist(err) {
		t.Fatal("README.md served")
	}
	if target, _ := os.Readlink(link); target != tip.Dir {
		t.Fatalf("link → %s, want %s", target, tip.Dir)
	}

	// Same head: no change, no error.
	if _, changed, err := s.Sync(ctx, "main"); err != nil || changed {
		t.Fatalf("unchanged: changed=%v err=%v", changed, err)
	}

	// New unsigned tip: refused, link stays on c1.
	r.commit(t, "main", map[string]string{"machines/aa-bb/machine.yaml": "role: evil\n"}, nil)
	if _, changed, err := s.Sync(ctx, "main"); !errors.Is(err, ErrUnsigned) || changed {
		t.Fatalf("unsigned update: changed=%v err=%v", changed, err)
	}
	if got := readFile(t, filepath.Join(link, "machines/aa-bb/machine.yaml")); got != "role: worker\n" {
		t.Fatalf("after refused update: %q", got)
	}
	if s.Current().Hash != c1.Hash.String() {
		t.Fatal("current moved on refused tip")
	}

	// Signed update with a Decrypt hook that sees the staging tree;
	// previous dir kept, link moved.
	files2 := map[string]string{"machines/aa-bb/machine.yaml": "role: cp\n", "clusters/h/secrets.yaml.age": "ct"}
	var decrypted string
	s.Decrypt = func(dir string) error {
		decrypted = dir
		return os.WriteFile(filepath.Join(dir, "clusters/h/secrets.yaml"), []byte("pt"), 0o600)
	}
	c2 := r.commit(t, "main", files2, owner)
	tip, changed, err = s.Sync(ctx, "main")
	if err != nil || !changed || tip.Hash != c2.Hash.String() {
		t.Fatalf("update: changed=%v err=%v tip=%+v", changed, err, tip)
	}
	if !strings.HasSuffix(decrypted, ".tmp") {
		t.Fatalf("decrypt ran on %s, want staging", decrypted)
	}
	if got := readFile(t, filepath.Join(link, "clusters/h/secrets.yaml")); got != "pt" {
		t.Fatalf("plaintext %q", got)
	}
	if _, err := os.Stat(filepath.Join(base, "talos-"+c1.Hash.String())); err != nil {
		t.Fatal("previous tree pruned too early")
	}

	// Decrypt failure: nothing moves.
	s.Decrypt = func(string) error { return errors.New("no master") }
	c3 := r.commit(t, "main", files2, owner)
	if _, changed, err := s.Sync(ctx, "main"); err == nil || changed {
		t.Fatalf("decrypt fail: changed=%v err=%v", changed, err)
	}
	if s.Current().Hash != c2.Hash.String() {
		t.Fatal("current moved on decrypt failure")
	}
	if _, err := os.Stat(filepath.Join(base, "talos-"+c3.Hash.String()+".tmp")); !os.IsNotExist(err) {
		t.Fatal("staging left behind")
	}

	// Third good tip: c1's tree is pruned, c2's kept.
	s.Decrypt = nil
	c4 := r.commit(t, "main", files2, owner)
	if _, changed, err := s.Sync(ctx, "main"); err != nil || !changed {
		t.Fatalf("third: changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(filepath.Join(base, "talos-"+c1.Hash.String())); !os.IsNotExist(err) {
		t.Fatal("c1 tree not pruned")
	}
	if _, err := os.Stat(filepath.Join(base, "talos-"+c2.Hash.String())); err != nil {
		t.Fatal("c2 tree pruned")
	}
	if _, err := os.Stat(filepath.Join(base, "talos-"+c4.Hash.String())); err != nil {
		t.Fatal("c4 tree missing")
	}

	// A different ref with the same content is a change (Ref is part
	// of identity: the /status override must be visible).
	r.heads["exp"] = r.heads["main"]
	if tip, changed, err := s.Sync(ctx, "exp"); err != nil || !changed || tip.Ref != "exp" {
		t.Fatalf("ref switch: changed=%v err=%v tip=%+v", changed, err, tip)
	}
}

func TestMaterializeRefusesSymlink(t *testing.T) {
	owner, ownerPub := newKey(t)
	r := newRepo()
	s := &Syncer{Fetch: r, Signers: signersFor("o", ownerPub), Base: t.TempDir(), Link: "talos"}
	r.commit(t, "main", map[string]string{"machines/x": "->/etc/passwd"}, owner)
	if _, changed, err := s.Sync(context.Background(), "main"); err == nil || changed {
		t.Fatalf("symlink: changed=%v err=%v", changed, err)
	}
}

// --- Against the real repo (format compatibility with `git commit -S`) ---

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	for d := wd; d != "/"; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "talos", SignersFile)); err == nil {
			if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
				return d
			}
		}
	}
	t.Skip("not inside the talos-config checkout")
	return ""
}

// TestVerifyRealHead verifies the checkout's HEAD against the committed
// talos/allowed-signers — the SSHSIG git writes must be what sshsig
// reads. Skips (does not fail) on an unsigned HEAD so a WIP commit does
// not break the suite; the pre-push hook is what refuses those.
func TestVerifyRealHead(t *testing.T) {
	root := repoRoot(t)
	signers, err := LoadSigners(filepath.Join(root, "talos"))
	if err != nil {
		t.Fatal(err)
	}
	gr, err := git.PlainOpen(root)
	if err != nil {
		t.Skip("PlainOpen:", err)
	}
	h, err := gr.ResolveRevision("HEAD")
	if err != nil {
		t.Skip("HEAD:", err)
	}
	c, err := gr.CommitObject(*h)
	if err != nil {
		t.Fatal(err)
	}
	if c.PGPSignature == "" {
		t.Skip("HEAD is unsigned")
	}
	p, err := Verify(c, signers)
	if err != nil {
		t.Fatalf("HEAD %s: %v", c.Hash, err)
	}
	t.Logf("HEAD %s signed by %s", c.Hash, p)
}

// TestRemoteLocal exercises the go-git Fetcher against the checkout
// over the file transport (needs a git binary for upload-pack; skipped
// in the nix sandbox).
func TestRemoteLocal(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git binary")
	}
	root := repoRoot(t)
	ref := strings.TrimSpace(run(t, root, "git", "rev-parse", "--abbrev-ref", "HEAD"))
	if ref == "HEAD" {
		t.Skip("detached HEAD")
	}
	want := strings.TrimSpace(run(t, root, "git", "rev-parse", ref))
	rem := Remote{URL: root}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h, err := rem.Head(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if h.String() != want {
		t.Fatalf("Head %s, want %s", h, want)
	}
	c, err := rem.Commit(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if c.Hash != h {
		t.Fatalf("Commit %s, want %s", c.Hash, h)
	}
	tree, _ := c.Tree()
	dst := t.TempDir()
	if err := Materialize(tree, dst, []string{"talos"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "talos", SignersFile)); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	return string(out)
}

func TestSyncSubdir(t *testing.T) {
	owner, ownerPub := newKey(t)
	r := newRepo()
	files := map[string]string{"talos/machines/aa/m.yaml": "x\n", "README.md": "no"}
	r.commit(t, "main", files, owner)
	ctx := context.Background()

	// Wrong subdir / paths that match nothing: refused, nothing linked.
	for _, s := range []*Syncer{
		{Fetch: r, Signers: signersFor("o", ownerPub), Base: t.TempDir(), Link: "talos", Subdir: "nope"},
		{Fetch: r, Signers: signersFor("o", ownerPub), Base: t.TempDir(), Link: "talos"}, // root: DefaultPaths absent
	} {
		if _, changed, err := s.Sync(ctx, "main"); err == nil || changed {
			t.Fatalf("subdir %q: changed=%v err=%v", s.Subdir, changed, err)
		}
		if _, err := os.Lstat(filepath.Join(s.Base, "talos")); err == nil {
			t.Fatalf("subdir %q: link created", s.Subdir)
		}
	}

	s := &Syncer{Fetch: r, Signers: signersFor("o", ownerPub), Base: t.TempDir(), Link: "talos", Subdir: "talos"}
	if _, changed, err := s.Sync(ctx, "main"); err != nil || !changed {
		t.Fatalf("subdir talos: changed=%v err=%v", changed, err)
	}
	if got := readFile(t, filepath.Join(s.Base, "talos", "machines/aa/m.yaml")); got != "x\n" {
		t.Fatalf("served %q", got)
	}
}
