//go:build iroh

package boot

import (
	"crypto/ed25519"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/marnyg/talos-config/actors/keyfile"
	"github.com/marnyg/talos-config/actors/relaygate"
	irohtransport "github.com/marnyg/talos-config/iroh-transport"
	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
)

// DefaultRelay is the home relay every actor binary defaults to: the
// talos hub's, reached by web-PKI HTTPS.
const DefaultRelay = "https://marnyg-talos-config.fly.dev"

// keyFile is the seed's name inside a -state dir.
const keyFile = "key"

// Logging sets the process-wide log shape: bare log lines, slog text
// on stderr, and the iroh core's level from SAP_LOG
// (trace|debug|info|warn).
func Logging() {
	log.SetFlags(0)
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	irohtransport.SetLogLevel(os.Getenv("SAP_LOG"))
}

// Actor binds priv on iroh (homed on relay; bindAddr "" = all
// interfaces, ephemeral port; streams are the stream-facet ALPNs the
// endpoint also accepts, irohtransport.Options.StreamALPNs) and
// returns an actor over it with its outbound seq seeded from the clock — every actors/cmd process
// outlives none of its counterparties' high-water marks (ADR-0006,
// actor.SeqBase). The caller closes the endpoint.
//
// claim says the relay is gated (protocol ADR-0012): the endpoint then
// tells the gate's proxy which id its connection is for
// (relaygate.Claim), without which a gated relay refuses the upgrade.
// It is fixed for the endpoint's life and is not a credential — the
// chain the actor presents is.
func Actor(priv ed25519.PrivateKey, relay, bindAddr string, streams []string, claim bool) (*actor.Actor, *irohtransport.Endpoint, error) {
	o := irohtransport.Options{BindAddr: bindAddr, Relay: relay, StreamALPNs: streams}
	if claim {
		o.RelayToken = relaygate.Claim(cert.NewEdSigner(priv).ActorID())
	}
	return bind(priv, o)
}

func bind(priv ed25519.PrivateKey, o irohtransport.Options) (*actor.Actor, *irohtransport.Endpoint, error) {
	ep, err := irohtransport.Bind(priv, o)
	if err != nil {
		return nil, nil, err
	}
	a := actor.New(cert.NewEdSigner(priv), ep)
	a.SeqBase = func() int64 { return time.Now().UnixMicro() }
	return a, ep, nil
}

// Persisted is the start of a long-lived actor whose id others hold
// chains to (the provisioner, the lighthouse, the laptop parent): its
// key persists in -state, and -print-id answers "what id do I name in
// the other side's -customer / -member" without starting anything.
type Persisted struct {
	name    string
	state   *string
	relay   *string
	bind    *string
	printID *bool
	// BindStreams are the stream-facet ALPNs Up binds beside the actor
	// ALPN (the parent's agent-events); set before Up.
	BindStreams []string
}

// Flags calls Logging and registers -state (default defaultState),
// -relay, -bind and -print-id on the command line for the binary name.
// Call before flag.Parse; then Up.
func Flags(name, defaultState string) *Persisted {
	Logging()
	return &Persisted{
		name:    name,
		state:   flag.String("state", defaultState, "state dir: key (persisted identity, minted on first run)"),
		relay:   flag.String("relay", DefaultRelay, "iroh home relay URL (also the dial hint for -lighthouse)"),
		bind:    flag.String("bind", "", "UDP bind address (default all interfaces, ephemeral port)"),
		printID: flag.Bool("print-id", false, "print this actor's id (minting the key if needed) and exit"),
	}
}

// State is the -state dir.
func (p *Persisted) State() string { return *p.state }

// Relay is the -relay URL.
func (p *Persisted) Relay() string { return *p.relay }

// Key loads (or mints) the key in -state; with -print-id it prints the
// id and exits 0. Call after flag.Parse, before validating the binary's
// own flags, so -print-id works with them absent.
func (p *Persisted) Key() ed25519.PrivateKey {
	if err := os.MkdirAll(*p.state, 0o700); err != nil {
		log.Fatal(err)
	}
	priv, minted, err := keyfile.Load(filepath.Join(*p.state, keyFile))
	if err != nil {
		log.Fatal(err)
	}
	if *p.printID {
		fmt.Println(cert.NewEdSigner(priv).ActorID())
		os.Exit(0)
	}
	if minted {
		slog.Info(p.name+": key minted", "state", *p.state)
	}
	return priv
}

// Up is Key, then binds homed on -relay and returns the actor, fatal
// on any error.
func (p *Persisted) Up() (*actor.Actor, *irohtransport.Endpoint) {
	// No claim: the long-lived actors still home on an ungated relay
	// (talos-config-ssau moves them).
	a, ep, err := Actor(p.Key(), *p.relay, *p.bind, p.BindStreams, false)
	if err != nil {
		log.Fatal(err)
	}
	return a, ep
}

// UpBehind binds the actor that RUNS the relay it homes on (the
// lighthouse with -relay-bin): homed on home, the relay's own loopback
// listener — it must not depend on its public one, and needs no claim
// there — while advertising -relay, the URL everyone else reaches the
// same relay at. Fatal on any error.
func (p *Persisted) UpBehind(priv ed25519.PrivateKey, home string) (*actor.Actor, *irohtransport.Endpoint) {
	a, ep, err := bind(priv, irohtransport.Options{BindAddr: *p.bind, Relay: home, AdvertiseRelay: *p.relay, StreamALPNs: p.BindStreams})
	if err != nil {
		log.Fatal(err)
	}
	return a, ep
}

// ViaRelay seeds a.Bootstrap so ids are dialled on -relay until a signed
// record of theirs is cached — the network bundle's endpoint on iroh
// (protocol ADR-0007 § Run live). Empty ids are skipped.
func (p *Persisted) ViaRelay(a *actor.Actor, ids ...cert.ActorID) {
	for _, id := range ids {
		if id == "" {
			continue
		}
		if a.Bootstrap == nil {
			a.Bootstrap = make(map[cert.ActorID][]string)
		}
		a.Bootstrap[id] = []string{irohtransport.TagRelay + *p.relay}
	}
}
