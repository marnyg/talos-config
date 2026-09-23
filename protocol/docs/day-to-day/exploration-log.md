# Exploration Log — sovereign-actor protocol

<!-- "What we've tried and ruled out." Prevents re-attempting dead ends across sessions.
     Granularity: strategy-level pivots only. Protocol scope only; the
     talos deployment's log is ../../../docs/day-to-day/exploration-log.md. -->

<!-- 2026-09-13: §M2 (nine ruled-out alternatives, grill-design
     2026-09-12) pruned — ADR-0001 is Accepted and built (0bc.2.1–.6).
     The rulings live in technical/adrs/0001-*.md and the glossary
     (Envelope, Invocation, Reply, Location record, Renewal beat,
     Facet, Transport, Mailbox). Recover from git history if needed. -->

## M4 spawn — birth handshake (grill-design 2026-09-21)

Ruled out for the newborn's first message to its parent:

- **Multiplex on `#frontdoor`** (`{kind: birth, nonce}` in the payload).
  Works, but the handler that answers strangers' knocks and the one
  that mints a child's whole authority set should not be one function.
- **Intro as a P-signed cert** (`{aud: "*", facet: #birth, cav.nonce}`)
  replacing the raw nonce. Seductive ("one primitive") but
  `Frontdoor()` already rules aud-`*` without postage malformed — the
  compensating rule for a bearer-ish cert is postage, not a nonce; a
  verifier-understood `cav.nonce` grows the closed caveat vocabulary
  (invariant 5) for a one-shot facet; and a bearer cert is still a
  bearer token in cert clothing.

Chosen: a dedicated **`#birth` facet** behind an aud-`*` consent of the
same shape as the frontdoor (postage required, so the pre-mailbox
`refusesUnstamped` guard applies); the nonce rides in the payload as
pure correlation against the pending-spawn table, granting nothing.
The child pays one PoW stamp to its own parent at birth.

Ruled out: **a new key may claim a spent nonce inside the birth
window** ("last-come-wins", to rescue a restarted pod with a fresh
keypair). P cannot distinguish a restarted honest child from an
impersonator who read the deployment params; it would hand the
impersonation hole a second entrance *after* the real child was born.
Chosen: the nonce **binds to the first key that presents it** for the
rest of the window — same key re-knocking gets the kit re-issued
(idempotent), any other key is refused. A newborn whose key does not
survive a restart is a dead newborn; the k8s adapter runs
`restartPolicy: Never`, `backoffLimit: 0`. Surviving a reboot is the
actor's own business (persist its own key/state) — the protocol does
not reason about key persistence.

**Where P is, from the intro.** Ruled out for v0: *raw endpoints*
(the sketch's literal text) — a signed reach-me-at record is the same
information the runtime already knows how to validate, cache and
expire, so the child's first `Send` takes the ordinary `hints` path
instead of a special bootstrap dial. *Lighthouse rendezvous at birth*
is **deferred, not ruled out**: `#lookup` needs a chain rooted at the
lighthouse, so it would mean a delegated lookup-cap in the intro; kept
as the additive upgrade (one more cert in the intro) for the case
where P moves inside the birth window — today that birth simply fails
and the funding tree GCs it.

**Starter kit.** Ruled out: a protocol-mandated parent-facing facet on
the child (`#stop`/`#ping`) — stopping is the lease handle's job by
design, and P→C authority is the child's own consent (invariant 3),
never shipped in the kit. Ruled out: a `funding`/`payment` slot in the
v0 kit — reserving a field for a rail not yet picked (M5). Ruled out:
reusing `cert.Bundle` (that is the stream-facet bundle-on-connect).

**Provider seam.** Ruled out: an *active* seam (`Spawn`/`Kill` only,
parent kills on a missed beat) — a dead parent would leave k8s
children running forever, the inverse of a funding tree. Chosen:
*passive* leases with a deadline that renewal extends
(`Spawn(spec, until)` / `Extend` / `Kill`); the birth window is the
first deadline, so failed births need no parent-side reaping.

**Extend trigger.** Ruled out: an `Actor.OnRenew` runtime hook (a
callback, not runtime state — strains ADR-0005's second test) and a
second beat (`#heartbeat` / spawner poll — the sketch refuses a second
loop shape). Chosen: `protocol/spawn` decorates the installed `#renew`
handler through `AcceptTable`; deadline = the renewed cert's `exp`.

**M4 acceptance.** Ruled out: *protocol-only* (fake provider, no
substrate) and *one real substrate* (k8s only). Chosen: fake provider
in the protocol tests **plus two real adapters, k8s Job and docker** —
two substrates force the child implementation to be self-contained in
its image and keep the provider seam honest for later platforms
(Akash). Owner's call 2026-09-21.

**Provider as a library → provisioner as an actor** (owner's sketch,
2026-09-21). Ruled out: the parent holding a Go `Provider` interface
and platform credentials (kubeconfig on the laptop). Chosen: the
*spawner* (parent library, knows actors) messages a *provisioner*
(platform actor, knows leases: `#spawn`/`#extend`/`#kill`), which
drives the platform through a `Driver`. Ruled out: the provisioner
tracking birth (observability) — a provisioner that needs zero
protocol knowledge beyond "actor with three facets" is the one third
parties can run, and the provisioner already sees the intro; it need
not also be told when the handshake succeeded. Ruled out for docker
lapse: adapter-side timers (die with the parent) and entrypoint
wrappers (deadline smuggled into the image). Chosen: the child
self-lapses on "no live edge"; docker sweeps orphans by label.
