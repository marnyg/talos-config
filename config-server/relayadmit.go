package main

import (
	"fmt"
	"slices"
	"time"

	"github.com/marnyg/talos-config/protocol/cert"
)

// relayColdWindow is how long after process start the relay admits
// everyone regardless of what the hub knows (talos-config-5gz, ruled
// 2026-10-17). The hub is relay-only by construction (ADR-0022): a
// remote member can only reach #bundle — the beat that witnesses it —
// through this relay, so a freshly deployed hub with an empty beat
// cache would otherwise lock every remote member out forever (the
// cold-cache trap). Five minutes is a beat interval with margin.
const relayColdWindow = 5 * time.Minute

// relayAdmitter decides relay admission from the hub's state. Inputs
// are injected so the rule is unit-testable without a live Issuer.
//
// Cold ⇒ open: the hub's directory is not yet trustworthy as a gate —
// it is sealed or nagging (beats are refused, so nothing can warm the
// cache), it has witnessed nobody, or it started less than
// relayColdWindow ago. Everyone is admitted, exactly as before 5gz.
//
// Warm ⇒ gated: the hub's own endpoint (it homes on its own relay) and
// the witnessed, non-blocklisted members. The blocklist is already
// applied inside NameMap(); a listed member is refused the relay on
// the same beat its certs stop renewing (j0b). Admission is a
// transport courtesy from a safe-to-lose cache (ADR-0019), never an
// authorization: losing the cache degrades to "open", never to a
// wrong grant.
//
// Gate off by default (2026-10-17, the day it went live): the
// sovereign-actor plane — sap-provisioner, sap-lighthouse, the laptop
// parent and every child, which homes where its parent is homed
// (actors/cmd/child) — holds protocol keys, not member certs, and the
// hub is relay-only by construction (ADR-0022), so a warm gate cut
// the plane off at the first reconnect (`The relay denied our
// authentication`). Until the protocol has an admission story for
// those actors (spike talos-config-5gz's successor), Admit computes
// the verdict and admits anyway; the mode on /status says `observing`
// when the last connect was a non-member the gate would have refused.
// Set gate to enforce.
type relayAdmitter struct {
	gate    bool                  // enforce the warm verdict; false ⇒ observe only
	self    cert.ActorID          // the hub's own endpoint, always admitted
	members func() []cert.ActorID // witnessed, off-blocklist member ids
	serving func() bool           // issuer.Serving() == nil
	started time.Time
	now     func() time.Time
}

// newRelayAdmitter wires the rule to a live hub.
func newRelayAdmitter(hub *hubManager, started time.Time) *relayAdmitter {
	iss := hub.issuer
	return &relayAdmitter{
		self: iss.ID(),
		members: func() []cert.ActorID {
			entries := iss.NameMap()
			ids := make([]cert.ActorID, 0, len(entries))
			for _, e := range entries {
				ids = append(ids, cert.ActorID(e.Member.Aud))
			}
			return ids
		},
		serving: func() bool { return iss.Serving() == nil },
		started: started,
		now:     time.Now,
	}
}

// Admit is the relayHook.Admit function: ok and the mode /status shows.
func (a *relayAdmitter) Admit(id cert.ActorID) (bool, string) {
	if a.now().Before(a.started.Add(relayColdWindow)) {
		return true, "open (cold: <5 min since start)"
	}
	if !a.serving() {
		return true, "open (cold: hub sealed or nagging)"
	}
	members := a.members()
	if len(members) == 0 {
		return true, "open (cold: no witnessed members)"
	}
	if id == a.self || slices.Contains(members, id) {
		return true, fmt.Sprintf("gated (%d members)", len(members))
	}
	if a.gate {
		return false, fmt.Sprintf("gated (%d members)", len(members))
	}
	return true, fmt.Sprintf("observing (%d members; non-member admitted)", len(members))
}

// relayHookFor builds the supervisor's hook for a hub listening on
// port: the loopback URL the child POSTs to, a fresh per-process
// bearer, and the admission rule above.
func relayHookFor(hub *hubManager, port int, started time.Time) (relayHook, error) {
	token, err := newRelayToken()
	if err != nil {
		return relayHook{}, err
	}
	return relayHook{
		URL:   fmt.Sprintf("http://127.0.0.1:%d%s", port, relayAccessPath),
		Token: token,
		Admit: newRelayAdmitter(hub, started).Admit,
	}, nil
}
