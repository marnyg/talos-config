# ADR-0007: Mesh cert group + derived source address as admin-route authentication

- Status: Accepted _(supersedes ADR-0003; implemented with phase 2 step 3)_
- Date: 2026-07-30
- Revised by: ADR-0016 / ADR-0017 (Proposed, 2026-09-03) — under
  Mesh v3 the group + derived-source-address inference is replaced by
  `authorize()` over the caller's presented cert chain; the gateway
  injects the verified identity as a header. Network-layer only; app
  sessions stay on ADR-0010.

## Context and Problem Statement

ADR-0003 authenticated the hub's tunnel `/config` route by wg0 source
address under cryptokey routing. Phase 2 deleted wg0; the route now
lives on the hub's nebula netstack listener (`serveMeshHTTP`). What
authenticates it there, with the same zero-state, zero-friction
properties?

## Decision Drivers

- Same as ADR-0003: invariants 1 and 2, routine `apply` without wallet
  friction, composed configs never on the public listener.
- ADR-0003's own consequence clause: "Mesh v2 must carry the property
  over."

## Considered Options

### Option A: Cert-group firewall alone

Nebula evaluates firewall rules against the groups in a peer's
certificate; the hub admits tcp/80 only from `group: admins`
(`hubNebulaConfig`). The group is CA-signed at enrollment, so a peer
that merely reaches the hub cannot claim it.

- Pros: enforcement below the HTTP layer; predicate signed by the CA.
- Cons: one layer; a bug in rule rendering or a too-generous group
  assignment silently exposes every machine's secrets.

### Option B: Derived source-address gate alone

The listener checks `r.RemoteAddr` against the admins-group overlay
addresses derived from the master (`adminMeshIPs`). Nebula binds a
cert's overlay address at mint time, so a valid tunnel cannot spoof
another member's address.

- Pros: exact ADR-0003 mechanism, transplanted.
- Cons: also one layer; soundness rests on nebula's addr↔cert binding
  alone.

### Option C: Both (chosen)

Firewall admits only the admins group; the handler additionally gates
on the derived admin address set. Both predicates reduce to the same
wallet-rooted derivation chain — no new state, no new trust.

## Decision Outcome

Chosen: **Option C**. The two layers fail independently: the firewall
protects against handler-level mistakes (a route mounted too broadly),
the source gate against firewall-rendering mistakes (a rule that
admits more than intended). Both are pure functions of (master, git),
preserving invariants 1 and 2.

### Consequences

- Same custody posture as ADR-0003: holding an enrolled admin device's
  mesh key is holding the admin surface.
- The checks are only sound on the overlay listener — the route must
  never be mounted on the public mux, and only `RemoteAddr` may be
  consulted.
- Media-group devices and machines are excluded by *both* layers;
  regrouping a device requires re-enrollment (the group is in the
  cert), which is the intended cost.
- The wg0-era enrollment (`wgup`, `/wg/enroll`) is gone; admin devices
  enroll via `nebup` (`/mesh/enroll`, wallet-signed nonce).

### Confirmation

`TestMeshHTTPOverOverlay` exercises the source gate through a real
overlay handshake (admin 200, media 403); `nebconf_test.go` pins the
firewall rule. Verified live 2026-07-30: `nix run .#apply` fetches
from `http://10.42.0.1` as the enrolled laptop. Invalidated if the
mesh ever admits peers whose overlay address is not bound to a
wallet-derived identity.
