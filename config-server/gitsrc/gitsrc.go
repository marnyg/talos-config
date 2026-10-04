// Package gitsrc makes a signed git tip the hub's talos/ tree
// (talos-config-r4fw → ra8k/k9h7). Today the fly image carries a
// snapshot of talos/ taken at build time, so a patch.yaml edit costs a
// build, a deploy and a re-unseal, and main runs ahead of what is
// served. The hub already reads --root per request; this package
// replaces what sits under --root, on a trigger, with the tree of a
// commit it has verified.
//
// Trust: GitHub is a transport, not a root of trust (invariant 3). A
// tip is served only if its SSHSIG verifies against the owner keys in
// talos/allowed-signers — the hub's BAKED copy of that file, never the
// fetched one. The tip's signature attests the whole tree, so only the
// tip is checked. Anything else fails closed: the previous tree stays.
//
// Invariant 2 is honoured, not bent: git is compiler input and the hub
// is the compiler. Receivers still decide from certs alone.
//
// Pure Go, C-free: go-git for the wire, hiddeco/sshsig for the
// signature. Testable without a git binary (Fetcher is an interface; the
// go-git remote is one implementation).
package gitsrc

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/hiddeco/sshsig"
	"golang.org/x/crypto/ssh"
)

// SignersFile is the allowed_signers file beside age-recipient.txt in
// talos/: the owner's commit-signing keys, in OpenSSH's
// "<principal> [options] <keytype> <key>" format.
const SignersFile = "allowed-signers"

// Namespace is the SSHSIG namespace git signs commits under.
const Namespace = "git"

// DefaultPaths is the subtree of talos/ the hub serves: what
// machines.Load/BuildConfig, policy.Load and the age decryption read.
// Not talosconfig.age (admin creds, fly never needs them) and not
// extensions/ (image build inputs) — the same cut fly/image.nix makes.
var DefaultPaths = []string{
	"base", "clusters", "hardware", "machines",
	"mesh-policy-v3.yaml", "mesh-blocklist-v3.txt",
	"age-recipient.txt", SignersFile,
}

// Signer is one line of allowed_signers.
type Signer struct {
	Principal string
	Key       ssh.PublicKey
}

// ParseSigners reads allowed_signers content. Lines starting with #
// and blank lines are ignored; options between the principal and the
// key type are skipped (none are honoured — no cert-authority, no
// namespaces, no validity windows; the file is the owner's keys, flat).
func ParseSigners(b []byte) ([]Signer, error) {
	var out []Signer
	sc := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 3 {
			return nil, fmt.Errorf("allowed_signers line %d: want <principal> <keytype> <key>", n)
		}
		i := 1
		for i < len(f) && !isKeyType(f[i]) {
			i++
		}
		if i+1 >= len(f) {
			return nil, fmt.Errorf("allowed_signers line %d: no key", n)
		}
		pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(strings.Join(f[i:], " ")))
		if err != nil {
			return nil, fmt.Errorf("allowed_signers line %d: %w", n, err)
		}
		for _, p := range strings.Split(f[0], ",") {
			out = append(out, Signer{Principal: p, Key: pub})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("allowed_signers: no keys")
	}
	return out, nil
}

func isKeyType(s string) bool {
	return strings.HasPrefix(s, "ssh-") || strings.HasPrefix(s, "ecdsa-sha2-") || strings.HasPrefix(s, "sk-")
}

// LoadSigners reads <root>/allowed-signers.
func LoadSigners(root string) ([]Signer, error) {
	b, err := os.ReadFile(filepath.Join(root, SignersFile))
	if err != nil {
		return nil, err
	}
	return ParseSigners(b)
}

// ErrUnsigned is returned for a commit without a signature header.
var ErrUnsigned = errors.New("commit is not signed")

// ErrUntrusted is returned when the commit's SSHSIG verifies under no
// allowed key (or is not an SSHSIG at all).
var ErrUntrusted = errors.New("commit signature matches no allowed signer")

// Verify checks c's SSHSIG against signers and returns the principal
// whose key signed it. The signed message is the commit object minus
// its gpgsig header, exactly what `git verify-commit` hands ssh-keygen.
func Verify(c *object.Commit, signers []Signer) (string, error) {
	if c.PGPSignature == "" {
		return "", ErrUnsigned
	}
	sig, err := sshsig.Unarmor([]byte(c.PGPSignature))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUntrusted, err)
	}
	obj := &plumbing.MemoryObject{}
	if err := c.EncodeWithoutSignature(obj); err != nil {
		return "", err
	}
	rd, err := obj.Reader()
	if err != nil {
		return "", err
	}
	msg, err := io.ReadAll(rd)
	if err != nil {
		return "", err
	}
	for _, s := range signers {
		if !bytes.Equal(s.Key.Marshal(), sig.PublicKey.Marshal()) {
			continue
		}
		if err := sshsig.Verify(bytes.NewReader(msg), sig, s.Key, sig.HashAlgorithm, Namespace); err != nil {
			return "", fmt.Errorf("%w: %v", ErrUntrusted, err)
		}
		return s.Principal, nil
	}
	return "", ErrUntrusted
}

// Fetcher resolves a ref at a remote and fetches its tip. The go-git
// implementation is Remote; tests substitute a storer-backed fake.
type Fetcher interface {
	// Head returns the hash ref points at, without fetching objects.
	Head(ctx context.Context, ref string) (plumbing.Hash, error)
	// Commit fetches the tip of ref (with its tree) and returns it.
	Commit(ctx context.Context, ref string) (*object.Commit, error)
}

// Remote is the go-git Fetcher: Head is an ls-remote, Commit a shallow
// single-branch clone into memory (the repo is a few MB; talos/ is
// ~100 KB, and a clone happens only when Head moved).
type Remote struct {
	URL string
}

func (r Remote) Head(ctx context.Context, ref string) (plumbing.Hash, error) {
	rem := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{Name: "origin", URLs: []string{r.URL}})
	refs, err := rem.ListContext(ctx, &git.ListOptions{})
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("ls-remote %s: %w", r.URL, err)
	}
	want := plumbing.NewBranchReferenceName(ref)
	for _, rf := range refs {
		if rf.Name() == want || rf.Name() == plumbing.ReferenceName(ref) {
			return rf.Hash(), nil
		}
	}
	return plumbing.ZeroHash, fmt.Errorf("ref %q not at %s", ref, r.URL)
}

func (r Remote) Commit(ctx context.Context, ref string) (*object.Commit, error) {
	repo, err := git.CloneContext(ctx, memory.NewStorage(), nil, &git.CloneOptions{
		URL:           r.URL,
		ReferenceName: plumbing.NewBranchReferenceName(ref),
		SingleBranch:  true,
		Depth:         1,
		NoCheckout:    true,
		Tags:          git.NoTags,
	})
	if err != nil {
		return nil, fmt.Errorf("clone %s@%s: %w", r.URL, ref, err)
	}
	head, err := repo.Head()
	if err != nil {
		return nil, err
	}
	return repo.CommitObject(head.Hash())
}

// Materialize writes the named top-level paths of tree under dst.
// Missing paths are skipped (a repo without a blocklist is legal);
// symlinks and submodules are refused — nothing in talos/ is either,
// and a symlink out of the tree is exactly the kind of surprise a
// fetched tree must not spring.
func Materialize(tree *object.Tree, dst string, paths []string) error {
	n := 0
	for _, p := range paths {
		e, err := tree.FindEntry(p)
		if errors.Is(err, object.ErrEntryNotFound) || errors.Is(err, object.ErrDirectoryNotFound) {
			continue
		}
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		if err := writeEntry(tree, e, filepath.Join(dst, p)); err != nil {
			return err
		}
		n++
	}
	if n == 0 {
		return fmt.Errorf("none of %v exist in the tree (wrong subdir?)", paths)
	}
	return nil
}

func writeEntry(parent *object.Tree, e *object.TreeEntry, dst string) error {
	switch e.Mode {
	case filemode.Dir:
		sub, err := parent.Tree(e.Name)
		if err != nil {
			return fmt.Errorf("%s: %w", dst, err)
		}
		if err := os.MkdirAll(dst, 0o700); err != nil {
			return err
		}
		for i := range sub.Entries {
			if err := writeEntry(sub, &sub.Entries[i], filepath.Join(dst, sub.Entries[i].Name)); err != nil {
				return err
			}
		}
		return nil
	case filemode.Regular, filemode.Executable, filemode.Deprecated:
		f, err := parent.TreeEntryFile(e)
		if err != nil {
			return fmt.Errorf("%s: %w", dst, err)
		}
		rd, err := f.Reader()
		if err != nil {
			return err
		}
		defer rd.Close()
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		mode := os.FileMode(0o600)
		if e.Mode == filemode.Executable {
			mode = 0o700
		}
		w, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, rd); err != nil {
			w.Close()
			return err
		}
		return w.Close()
	default:
		return fmt.Errorf("%s: refusing mode %s", dst, e.Mode)
	}
}

// Swap atomically points link at dir: a fresh symlink beside it, then
// rename over. Readers opening through link see one tree or the other,
// never a mix of directory entries — though a request that opens
// several files should resolve the link once (filepath.EvalSymlinks)
// to read them all from the same tree.
func Swap(link, dir string) error {
	tmp := link + ".new"
	_ = os.Remove(tmp)
	if err := os.Symlink(dir, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Tip describes a served commit.
type Tip struct {
	Hash   string
	Ref    string
	Signer string    // principal from allowed_signers
	When   time.Time // committer time
	Dir    string    // where its tree was materialized
}

// Syncer turns a verified tip into the tree under Link.
//
//	base/
//	  talos           -> talos-<sha>   (Link; what --root points at)
//	  talos-<sha>/                      (Tip.Dir)
//	  talos-<older>/                    (kept until the next swap)
type Syncer struct {
	Fetch   Fetcher
	Signers []Signer // the baked allowed_signers, parsed
	Base    string   // directory holding Link and the per-tip trees
	Link    string   // symlink name under Base, e.g. "talos"
	Subdir  string   // subtree of the repo to serve, e.g. "talos" ("" = the root)
	Paths   []string // nil = DefaultPaths, relative to Subdir

	// Decrypt, when set, runs on the new tree before the swap (the
	// .age-at-unseal step, hubseal.go); its failure aborts the swap.
	Decrypt func(dir string) error

	current *Tip
}

// Current is the last tip swapped in, nil before the first Sync.
func (s *Syncer) Current() *Tip { return s.current }

// Sync fetches ref's tip; if it is new, verifies it, materializes its
// tree, decrypts, swaps, and prunes trees older than the previous one.
// changed reports whether Link moved. Any error leaves Link untouched.
func (s *Syncer) Sync(ctx context.Context, ref string) (tip *Tip, changed bool, err error) {
	head, err := s.Fetch.Head(ctx, ref)
	if err != nil {
		return s.current, false, err
	}
	if s.current != nil && s.current.Hash == head.String() && s.current.Ref == ref {
		return s.current, false, nil
	}
	c, err := s.Fetch.Commit(ctx, ref)
	if err != nil {
		return s.current, false, err
	}
	if c.Hash != head {
		// ls-remote and clone raced a push; the next poll resolves it.
		return s.current, false, fmt.Errorf("ref %s moved during fetch (%s → %s)", ref, head, c.Hash)
	}
	principal, err := Verify(c, s.Signers)
	if err != nil {
		return s.current, false, fmt.Errorf("%s@%s: %w", ref, c.Hash, err)
	}
	tree, err := c.Tree()
	if err != nil {
		return s.current, false, err
	}
	if s.Subdir != "" {
		if tree, err = tree.Tree(s.Subdir); err != nil {
			return s.current, false, fmt.Errorf("%s@%s: subdir %s: %w", ref, c.Hash, s.Subdir, err)
		}
	}
	paths := s.Paths
	if paths == nil {
		paths = DefaultPaths
	}
	dir := filepath.Join(s.Base, s.Link+"-"+c.Hash.String())
	staging := dir + ".tmp"
	_ = os.RemoveAll(staging)
	if err := Materialize(tree, staging, paths); err != nil {
		_ = os.RemoveAll(staging)
		return s.current, false, err
	}
	if s.Decrypt != nil {
		if err := s.Decrypt(staging); err != nil {
			_ = os.RemoveAll(staging)
			return s.current, false, fmt.Errorf("decrypt %s: %w", c.Hash, err)
		}
	}
	_ = os.RemoveAll(dir)
	if err := os.Rename(staging, dir); err != nil {
		_ = os.RemoveAll(staging)
		return s.current, false, err
	}
	if err := Swap(filepath.Join(s.Base, s.Link), dir); err != nil {
		return s.current, false, err
	}
	prev := s.current
	s.current = &Tip{Hash: c.Hash.String(), Ref: ref, Signer: principal, When: c.Committer.When, Dir: dir}
	s.prune(prev)
	return s.current, true, nil
}

// prune removes per-tip trees other than the current and previous one.
func (s *Syncer) prune(prev *Tip) {
	entries, err := os.ReadDir(s.Base)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !strings.HasPrefix(name, s.Link+"-") {
			continue
		}
		p := filepath.Join(s.Base, name)
		if p == s.current.Dir || (prev != nil && p == prev.Dir) {
			continue
		}
		_ = os.RemoveAll(p)
	}
}
