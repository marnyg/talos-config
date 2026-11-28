# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-11-28): the protocol network as its own surface (`k7sd`, `qx5c`)

- Ruled by the owner, all Accepted: invariant 5 scoped to the talos
  deployment (two dependency rules), the goal "the hub is one consumer
  among many", **ADR-0051** (protocol network = its own fly app:
  `iroh-relay` + `cmd/lighthouse`; the hub keeps its relay for
  members), **protocol ADR-0012** (relay admission = a `relay` chain
  the actor presents; a newborn presents a wildcard link from its
  intro). `qx5c` closed, decision `9f7n`.
- Built: **`actors/relaygate`** (`bal3`) — verify, the `id → exp`
  table, the relay access hook, the proxy that ends a connection at
  expiry, and the client side (`Prepare`, `Post`, `Claim`). Untagged
  tests plus `TestGateInFrontOfRelay` against the real relay
  (`scripts/test-iroh.sh actors`; `actors-bin` now runs it too).
- `irohtransport.Options` gained `RelayToken`. Nothing is deployed and
  no binary uses the gate yet.

## Loose threads

- The gate's rollback floor is its start, not the last presentation:
  `relay`/`publish` chain links do not feed the clock mark (`w0ur`,
  protocol invariant 9's letter). Affects the lighthouse too.
- The id claim in the relay bearer is unsigned (ADR-0012, not
  decided); also open there: a stranger door at the relay, a child
  delegating onward, a per-client byte limit.
- A relay bearer token cannot be replaced on a live iroh 1.1.0
  endpoint — it carries a claim, never a credential.
- The caller connects to the foreign relay, so an enforcing hub gate
  blocks actor → member dials. No such edge exists today.
- `boot.ViaRelay` uses the actor's own `-relay` as the lighthouse dial
  hint; fine only while every actor shares the lighthouse's relay.
- Every relay test ran on loopback, not through fly's TLS proxy.
- `iroh-transport`'s `TestRelayGate` (the spike's stub gate) now
  overlaps `actors/relaygate`'s tagged test; ADR-0012 cites both.
- **The working tree holds uncommitted Krea 2 / Mojo work**
  (`k8s/apps/comfyui/max-krea2/`, spike `j1kd`) from another session;
  this session did not touch or commit it.
- Carried over: ADR-0050 (any-port TCP) is still Proposed; the unused
  `longhorn-local` claims await the owner (`e6rx.8`).

## Suggested next steps

- `bpha`: the relay link in `protocol/spawn`'s intro and kit; then
  `0ken`: the child presents (before bind, after birth, every beat).
- The tasks filed under `k7sd`: the binary that runs gate + relay +
  lighthouse, the fly app, moving the provisioner and parent over,
  then enforcing the hub's member gate.
