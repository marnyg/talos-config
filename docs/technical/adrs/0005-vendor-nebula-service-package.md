# ADR-0005: Vendor nebula's service package instead of importing it

- Status: Proposed _(records a decision implemented 2026-07-29)_
- Date: 2026-07-29

## Context and Problem Statement

The hub embeds nebula as a library and has no TUN device — it runs
`overlay.UserDevice` and pipes overlay packets into a gVisor netstack, via
nebula's own `service` package. The hub must serve DNS for
`*.mesh.internal` on the overlay (ADR-0002), which needs a UDP listener
on its overlay address.

Nebula's built-in lighthouse DNS (`serve_dns`) cannot do this: it binds a
kernel socket and therefore needs a TUN. And `service.Listen` rejects
anything that is not TCP. So the hub needs UDP on the netstack by some
other route.

Diagnosis matters here, because the obvious framing ("the package is
TCP-only, so UDP support must be added") is wrong. Upstream's stack is
already fully UDP-capable: `service.New` registers `udp.NewProtocol` and
adds the overlay address to the NIC, and `DialContext` already handles
`udp`. The only real obstacle is that **`*stack.Stack` is an unexported
field with no accessor**, so an importer cannot bind an endpoint itself.
The capability is there; the handle is not.

## Decision Drivers

- The hub must answer DNS from its own overlay address; a wildcard bind
  or a forwarder answering for addresses it does not own would be a lie
  to resolvers.
- Precedent: `wgstack` is already a vendored trim of wireguard-go's
  `tun/netstack` for the *same underlying reason* — upstream keeps
  `*stack.Stack` unexported. Its package comment says so.
- Nebula is a security-relevant dependency that will need version bumps;
  whatever we do must not make those bumps expensive or risky.
- Phase 1 is on a schedule with kill criteria armed — a fix that depends
  on an upstream release cycle is not viable.

## Considered Options

### Option A: Upstream a patch exporting the stack (or a UDP Listen)

Send nebula a PR adding `Service.ListenUDP` or a stack accessor.

- Pros: no local copy; everyone benefits; the right long-term home.
- Cons: blocks phase 1 on someone else's review and release cadence.
  Also a real API-design question for upstream (exposing `*stack.Stack`
  leaks gVisor into their public surface), so it is not obviously a
  fast merge. Worth doing *later*, not instead.

### Option B: Fork nebula

Maintain a patched nebula.

- Pros: total freedom.
- Cons: wildly disproportionate — we need one listener, not a different
  nebula. Forking a security-relevant dependency means owning its
  security updates.

### Option C: Reimplement the netstack plumbing from scratch

Write our own `Control` → netstack bridge rather than adapting theirs.

- Pros: no copied code.
- Cons: the packet-pump goroutines, MTU handling and teardown ordering
  are exactly the fiddly parts worth *not* reinventing; we would diverge
  from upstream's behavior with no benefit.

### Option D: Vendor a trim of `service`, keeping upstream's shapes (chosen)

Copy the package into `config-server/nebstack/`, delete what the hub does
not use, and add `ListenUDP` plus `OverlayAddr`. Keep every retained
function byte-identical to upstream so the file stays diffable against
the next release.

- Pros: unblocks phase 1 immediately; ~1 file; mirrors the `wgstack`
  precedent so there is one pattern in the repo, not two; the diff
  against upstream stays small enough to re-apply by eye on a bump.
- Cons: a copy that must be re-checked on every nebula upgrade. Upstream
  bug fixes to `service` do not arrive automatically.

## Decision Outcome

Chosen: **Option D**, because the blocker is an access-modifier accident
rather than a missing capability, and the repo already has exactly this
pattern with exactly this cause (`wgstack`). Vendoring keeps the change
proportional: one listener added, upstream's logic untouched.

Deliberately **no UDP forwarder**. A forwarder catches datagrams the
stack has no endpoint for; `ListenUDP` binds a real endpoint on the hub's
own overlay address, so normal demultiplexing delivers to it. Answering
on addresses we do not own would, for DNS, be a lie.

### Consequences

- Every nebula bump needs `nebstack.go` re-diffed against
  `nebula/service/`. This is the recurring cost accepted; keeping the
  retained code verbatim is what makes it cheap, so **resist the
  temptation to tidy upstream's code** — divergence is the thing that
  makes a bump expensive.
- The trim is intentionally minimal (`New`, `DialContext`, `Listen`,
  `ListenUDP`, `Wait`, `Close`). Anything else the hub needs later should
  be copied from upstream rather than invented.
- Option A stays open and is still worth doing — upstreaming would delete
  this ADR's cost entirely. Filed as a follow-up, not a blocker.
- Tests live in an external test package so they can share the `nebtest`
  harness; that harness renders deliberately minimal configs, and the
  hub's real config is validated separately (`nebconf_test`).

### Confirmation

Confirmed by `TestListenUDPOverOverlay` and `TestMeshDNSOverOverlay`: a
peer resolves a name over a real handshake against the TUN-less hub,
which the stock package cannot serve. Both were mutation-verified —
dropping inbound overlay packets fails them — so they test the transport
rather than a loopback shortcut.

Invalidated if upstream exports the stack or adds UDP listening, at which
point this package should shrink to nothing. Also worth revisiting if the
trim starts drifting from upstream for reasons other than `ListenUDP` —
that would mean we are fork-shaped and should admit it.
