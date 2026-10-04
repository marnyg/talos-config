package main

// Served-from-git (talos-config-k9h7, spike r4fw): the tree under
// --root is a symlink the hub re-points at the verified tip of a git
// ref, instead of the snapshot the image was built with. --git-remote
// turns it on; the baked tree is the first target and the fallback —
// a fetch or verification failure leaves the last-good tree in place,
// never an empty one.
//
// The ref defaults to --git-ref (main) and can be overridden from
// /status for an experiment. The override is volatile — a safe-to-lose
// setting in ADR-0019's sense: a restart (and so a re-seal) drops back
// to the default, so an experiment cannot become the silent default.
// The signature requirement applies to whatever ref is served.
//
// Secrets: the .age files in a new tree are decrypted with the held
// master before the swap (fail-closed: an undecryptable tree is not
// served) and once more after it, idempotently, which closes the race
// with an unseal that decrypted the old tree in between. While sealed
// the swap happens anyway — policy and machines serve without the
// master — and the unseal decrypts whatever --root points at then.

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/marnyg/talos-config/config-server/gitsrc"
)

type gitSync struct {
	syncer     *gitsrc.Syncer
	remote     string
	defaultRef string
	poll       time.Duration
	root       string // the symlink

	mu      sync.Mutex
	ref     string // served ref: override or default
	lastErr error
	lastTry time.Time
	nudge   chan struct{}
}

// newGitSync sets up serving from remote@ref under root, which must be
// a symlink (fly/entrypoint.sh makes it one, pointing at the baked
// tree). The allowed signers are read from the tree root points at NOW
// — the baked copy — and never again: the trust anchor cannot come
// from the thing it anchors.
func newGitSync(root, remote, ref, subdir string, poll time.Duration, decrypt func(string) error) (*gitSync, error) {
	fi, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return nil, fmt.Errorf("--git-remote needs --root to be a symlink the hub can re-point (got a plain %s)", root)
	}
	signers, err := gitsrc.LoadSigners(root)
	if err != nil {
		return nil, fmt.Errorf("allowed signers from the baked tree: %w", err)
	}
	for _, s := range signers {
		log.Printf("git: tips signed by %s (%s) will be served", s.Principal, s.Key.Type())
	}
	return &gitSync{
		syncer: &gitsrc.Syncer{
			Fetch:   gitsrc.Remote{URL: remote},
			Signers: signers,
			Base:    filepath.Dir(root),
			Link:    filepath.Base(root),
			Subdir:  subdir,
			Decrypt: decrypt,
		},
		remote:     remote,
		defaultRef: ref,
		poll:       poll,
		root:       root,
		ref:        ref,
		nudge:      make(chan struct{}, 1),
	}, nil
}

// nudgeGap floors the time between two nudged syncs: the channel
// coalesces a burst into one pending kick, but without a floor a steady
// stream of unauthenticated POST /git/nudge would still cost one
// ls-remote per sync.
const nudgeGap = 10 * time.Second

// run polls until ctx ends; a nudge shortens the wait (to no less than
// nudgeGap after the previous attempt).
func (g *gitSync) run(ctx context.Context, after func()) {
	for {
		g.syncOnce(ctx, after)
		select {
		case <-ctx.Done():
			return
		case <-g.nudge:
			g.mu.Lock()
			wait := time.Until(g.lastTry.Add(nudgeGap))
			g.mu.Unlock()
			if wait > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(wait):
				}
			}
		case <-time.After(g.poll):
		}
	}
}

func (g *gitSync) syncOnce(ctx context.Context, after func()) {
	g.mu.Lock()
	ref := g.ref
	g.mu.Unlock()
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	tip, changed, err := g.syncer.Sync(cctx, ref)
	cancel()
	g.mu.Lock()
	g.lastErr = err
	g.lastTry = time.Now()
	g.mu.Unlock()
	switch {
	case err != nil:
		log.Printf("git: %s not served: %v", ref, err)
	case changed:
		log.Printf("git: serving %s@%s (signed by %s, %s)", tip.Ref, tip.Hash[:12], tip.Signer, tip.When.UTC().Format(time.RFC3339))
		if after != nil {
			after()
		}
	}
}

// kick asks for a sync now (a webhook, a /status action).
func (g *gitSync) kick() {
	select {
	case g.nudge <- struct{}{}:
	default:
	}
}

// setRef changes the served ref ("" = back to the default) and kicks.
func (g *gitSync) setRef(ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		ref = g.defaultRef
	}
	if strings.ContainsAny(ref, " \t\n~^:?*[\\") || strings.HasPrefix(ref, "-") || strings.Contains(ref, "..") {
		return fmt.Errorf("not a ref name: %q", ref)
	}
	g.mu.Lock()
	g.ref = ref
	g.mu.Unlock()
	g.kick()
	return nil
}

// statusLine is the /status row: what is served, by whose signature,
// how old, and the last failure if the served tree is behind the ref.
func (g *gitSync) statusLine(now time.Time) (line string, warn bool) {
	g.mu.Lock()
	ref, lastErr, lastTry := g.ref, g.lastErr, g.lastTry
	g.mu.Unlock()
	tip := g.syncer.Current()
	var b strings.Builder
	if tip == nil {
		b.WriteString("baked image tree (no verified tip yet)")
		warn = true
	} else {
		fmt.Fprintf(&b, "%s@%s signed by %s, committed %s ago", tip.Ref, tip.Hash[:12], tip.Signer, now.Sub(tip.When).Truncate(time.Minute))
		if tip.Ref != g.defaultRef {
			fmt.Fprintf(&b, " — OVERRIDE (default %s; a restart reverts)", g.defaultRef)
			warn = true
		}
	}
	if tip == nil || tip.Ref != ref {
		fmt.Fprintf(&b, " — switching to %s", ref)
	}
	if lastErr != nil {
		fmt.Fprintf(&b, " — last fetch %s ago failed: %v", now.Sub(lastTry).Truncate(time.Second), lastErr)
		warn = true
	} else if !lastTry.IsZero() {
		fmt.Fprintf(&b, " — checked %s ago", now.Sub(lastTry).Truncate(time.Second))
	}
	return b.String(), warn
}

// handleGitNudge: POST /git/nudge, unauthenticated. A GitHub push
// webhook (or anyone) may ask for a fetch now; the content is what is
// verified, not the caller, so the worst a stranger can do is make the
// hub ls-remote early. Rate-limited twice: the channel holds one pending
// kick, and run() spaces nudged syncs at least nudgeGap apart.
func (s *server) handleGitNudge(w http.ResponseWriter, r *http.Request) {
	if s.git == nil {
		http.NotFound(w, r)
		return
	}
	s.git.kick()
	w.WriteHeader(http.StatusAccepted)
}

// handleGitRef: POST /status/git-ref {ref} — the volatile override,
// behind the /status session like every other admin action there.
func (s *server) handleGitRef(w http.ResponseWriter, r *http.Request) {
	if !s.statusEnabled() || s.git == nil {
		http.NotFound(w, r)
		return
	}
	addr, ok := s.sessionAddr(r)
	if !ok {
		http.Error(w, "login first", http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	ref := r.FormValue("ref")
	if err := s.git.setRef(ref); err != nil {
		http.Redirect(w, r, "/status?msg="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	if ref == "" {
		ref = s.git.defaultRef
	}
	log.Printf("git: %s asked to serve %s", addr, ref)
	http.Redirect(w, r, "/status?msg="+url.QueryEscape("serving "+ref+" once its tip verifies"), http.StatusSeeOther)
}

// resolveRoot pins one tree for a request that reads several files:
// a swap between two opens would otherwise mix trees. Falls back to
// root itself when it is not a link (dev, tests).
func resolveRoot(root string) string {
	if r, err := filepath.EvalSymlinks(root); err == nil {
		return r
	}
	return root
}
