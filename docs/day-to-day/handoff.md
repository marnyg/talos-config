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
- Built, all on `main`, tested, **not deployed and unused by any
  manifest** (the running system is unchanged):
  - `actors/relaygate` (`bal3`): the gate — verify, the `id → exp`
    table, the relay's access hook, the proxy that ends a connection
    at expiry; client side `Prepare`/`Post`/`Presenter`/`Claim`.
  - `protocol/spawn` (`bpha`): `Spawner.Relay` puts a relay birth
    link in the intro and a `P→C relay` chain in the kit.
  - the child presents (`0ken`): `cmd/child` shows the gate its
    intro's link before it binds and claims its id; `child.Run`
    presents the kit's chain at birth and every beat
    (`Options.Present`); `cmd/parent`, `cmd/spawn` take `-relay-id`.
- Evidence: `TestBornThroughTheGate` (`actors/relaygate`, tagged) runs
  spawner → provisioner → child on the real relay behind the gate.
  `scripts/test-iroh.sh actors`; `actors-bin` runs it in the sandbox.

## Loose threads

- The gate's rollback floor is its start, not the last presentation:
  `relay`/`publish` chain links do not feed the clock mark (`w0ur`,
  protocol invariant 9's letter). Affects the lighthouse too.
- The id claim in the relay bearer is unsigned (ADR-0012, not
  decided); also open there: a stranger door at the relay, a child
  delegating onward, a per-client byte limit.
- The caller connects to the foreign relay, so an enforcing hub gate
  blocks actor → member dials. No such edge exists today.
- `boot.ViaRelay` uses the actor's own `-relay` as the lighthouse dial
  hint; fine only while every actor shares the lighthouse's relay.
- Every relay test ran on loopback, not through fly's TLS proxy.
- **The working tree holds uncommitted Krea 2 / Mojo work**
  (`k8s/apps/comfyui/max-krea2/`, spike `j1kd`) from another session;
  this session did not touch or commit it.
- Carried over: ADR-0050 (any-port TCP) is still Proposed; the unused
  `longhorn-local` claims await the owner (`e6rx.8`).

## Suggested next steps

- `gsps`: the binary that runs gate + relay + lighthouse (relay
  supervision; `-member` mints the relay consent, delegable for
  parents).
- Then `9nkh` (the fly app), `ssau` (move the sap plane: members bind
  with the claim, manifests get `-relay-id`), `e7f0` (enforce the
  hub's member gate).
