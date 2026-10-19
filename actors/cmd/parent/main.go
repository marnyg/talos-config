//go:build iroh

// Command parent is the agent backend of ADR-0037 (talos-config-ivof):
// the long-lived parent actor. It rents a child per session from a
// provisioner (#spawn → #birth → #task), is the sink of each child's
// event stream (the agent-events facet), keeps every session in its
// own store (SQLite+FTS5 on -state), and serves the list/watch/steer/
// search UI behind the person gate (an OIDC client of siwe-oidc).
//
//	parent -state DIR -print-id
//	parent -state DIR -lighthouse ed:… -provisioner-id ed:… -image ghcr.io/…@sha256:… \
//	       -listen :8080 -issuer http://auth.gw.mesh.internal -public-url http://agents.gw.mesh.internal
//
// Identity: -state holds the key (minted on first run; the provisioner's
// -customer names this id, so print it first), the store (parent.db,
// sessions/) and the cookie key (web.key). A durable-key actor (root
// invariant 2's actor-owned state): its children's edges survive a
// restart by re-adoption from the store (parent.Restore), not by
// re-birth. One holder at a time (the manifest is Recreate).
//
// Finding the provisioner (ADR-0007): #lookup at -lighthouse, first on
// -relay alone (actor.Bootstrap); the beat re-looks it up before the
// cached record expires, so an idle parent can still #spawn.
//
// The agent-events facet (child.FacetAgentEvents): every connection is
// decided by parent.Admit (VerifyChain over the preamble, the protocol's
// own rule) and served by parent.Handler through facethttp.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/marnyg/talos-config/actors/child"
	"github.com/marnyg/talos-config/actors/cmd/internal/boot"
	"github.com/marnyg/talos-config/actors/keyfile"
	"github.com/marnyg/talos-config/actors/parent"
	irohtransport "github.com/marnyg/talos-config/iroh-transport"
	"github.com/marnyg/talos-config/protocol/cert"
	"github.com/marnyg/talos-config/protocol/facethttp"
	"github.com/marnyg/talos-config/protocol/lighthouse"
	"github.com/marnyg/talos-config/protocol/spawn"
)

func main() {
	pb := boot.Flags("parent", "/var/lib/sap-parent")
	var lhFlag, provFlag boot.ID
	flag.Var(&lhFlag, "lighthouse", "lighthouse actor id to #lookup the provisioner at (this id must be one of its -member); dialled on -relay")
	flag.Var(&provFlag, "provisioner-id", "the provisioner's actor id")
	var (
		image     = flag.String("image", "", "agent child image by content digest (name@sha256:…); what every #spawn names")
		window    = flag.Duration("window", 5*time.Minute, "birth window: how long a child has to knock (pull time included)")
		kitTTL    = flag.Duration("kit-ttl", 10*time.Minute, "lifetime of a child's #renew chain per issue; keep above -window")
		beat      = flag.Duration("beat", 30*time.Second, "housekeeping interval: sweep, snapshot, retire, reach-me-at")
		locTTL    = flag.Duration("location-ttl", 10*time.Minute, "lifetime of each published reach-me-at; keep well above -beat")
		grace     = flag.Duration("grace", 2*time.Minute, "restart grace for adopted children; how long a settled child keeps its lease to ship")
		backoff   = flag.Duration("backoff", time.Minute, "wait before a task's second attempt, doubling per attempt (capped at 16×); spawned on the beat")
		listen    = flag.String("listen", ":8080", "the UI's HTTP listen address")
		issuer    = flag.String("issuer", "http://auth.gw.mesh.internal", "siwe-oidc issuer URL (as the browser and this process both reach it)")
		clientID  = flag.String("client-id", "agents", "the -client name siwe-oidc knows this UI by")
		publicURL = flag.String("public-url", "http://agents.gw.mesh.internal", "this UI's origin as the browser reaches it (the OIDC redirect is <public-url>/callback)")
		cookieTTL = flag.Duration("cookie-ttl", 7*24*time.Hour, "lifetime of a login")
	)
	flag.Parse()
	pb.BindStreams = []string{child.FacetAgentEvents}
	a, ep := pb.Up() // -print-id exits here
	defer ep.Close()
	lh, prov := lhFlag.ActorID(), provFlag.ActorID()
	if *image == "" || lh == "" || prov == "" {
		log.Fatal("need -image name@sha256:…, -lighthouse and -provisioner-id (or -print-id)")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := parent.Open(pb.State())
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	cookieKey, _, err := keyfile.Load(filepath.Join(pb.State(), "web.key"))
	if err != nil {
		log.Fatal(err)
	}

	pb.ViaRelay(a, lh)
	sp := spawn.New(a)
	sp.Provisioner = prov
	sp.Window = int64(window.Seconds())
	sp.KitTTL = int64(kitTTL.Seconds())
	p := parent.New(parent.Options{Actor: a, Spawner: sp, Store: store, Image: *image, Beat: *beat, Grace: *grace, Backoff: *backoff})
	lost, err := p.Restore()
	if err != nil {
		log.Fatal(err)
	}
	if len(lost) > 0 {
		slog.Warn("parent: sessions lost across the restart", "ids", lost)
	}

	// The provisioner's whereabouts: looked up now, then refreshed on
	// the beat before the cached record expires. A failure is a warning,
	// not fatal: the UI and the store still serve (a Submit fails at
	// #spawn with the reason), and a first deploy has to come up before
	// its id is in the lighthouse's -member and the provisioner's
	// -customer at all (the id is logged below; k8s/apps/sap-parent).
	lookup := func() error {
		lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		recs, err := lighthouse.Lookup(lctx, a, lh, prov)
		if err != nil {
			return err
		}
		rec, ok := recs[prov]
		if !ok {
			return errors.New("no live record (is the provisioner up and a -member there?)")
		}
		slog.Info("parent: #lookup ok", "lighthouse", lh, "provisioner", prov, "exp", rec.Loc.Exp)
		return nil
	}
	slog.Info("parent: id", "id", a.ID(), "state", pb.State())
	if err := lookup(); err != nil {
		slog.Warn("parent: #lookup failed; retrying on the beat", "lighthouse", lh, "provisioner", prov, "err", err)
	}

	// The agent-events facet.
	srv, ln := facethttp.Server(child.FacetAgentEvents, p.Handler())
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	go accept(ctx, ep, ln, p)

	// The UI.
	login := &parent.Login{Issuer: *issuer, ClientID: *clientID, RedirectURL: *publicURL + "/callback", Key: cookieKey.Seed(), TTL: *cookieTTL}
	web := &parent.Web{P: p, Store: store, Login: login}
	hs := &http.Server{Addr: *listen, Handler: web.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	defer hs.Close()

	publish := func() {
		if _, err := a.PublishLocation(int64(locTTL.Seconds())); err != nil {
			slog.Warn("parent: location", "err", err)
		}
	}
	publish()
	listenErr := make(chan error, 1)
	go func() { listenErr <- a.Listen(ctx) }()
	slog.Info("parent: up", "id", a.ID(), "provisioner", prov, "relay", pb.Relay(), "listen", *listen, "public", *publicURL)

	t := time.NewTicker(*beat)
	defer t.Stop()
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case err := <-listenErr:
			if err != nil && !errors.Is(err, context.Canceled) {
				log.Fatal(err)
			}
			break loop
		case <-t.C:
		}
		p.Beat(ctx)
		publish()
		if loc := a.GetLocation(prov); loc == nil || loc.Exp < a.Now()+2*int64(beat.Seconds()) {
			if err := lookup(); err != nil {
				slog.Warn("parent: #lookup", "lighthouse", lh, "err", err)
			}
		}
	}
	stop()
	p.Beat(context.Background()) // last snapshot
	slog.Info("parent: exit")
	_ = os.Stdout.Sync()
}

// accept runs the agent-events accept loop for the life of ctx.
func accept(ctx context.Context, ep *irohtransport.Endpoint, ln *facethttp.Listener, p *parent.Parent) {
	for {
		c, err := ep.AcceptConn(ctx)
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			pre, err := c.Preamble(ctx)
			if err != nil {
				return
			}
			if err := p.Admit(c.Peer(), pre); err != nil {
				slog.Warn("parent: agent-events: refused", "peer", c.Peer(), "err", err)
				_ = c.Refuse(ctx, err.Error())
				return
			}
			if err := c.Admit(ctx); err != nil {
				return
			}
			for {
				raw, err := c.Accept(ctx)
				if err != nil {
					return
				}
				if !ln.Push(ctx, raw, cert.Identity{Key: c.Peer()}, c.Peer()) {
					_ = raw.Close()
					return
				}
			}
		}()
	}
}
