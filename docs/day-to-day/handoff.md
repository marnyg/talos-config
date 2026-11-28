# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-11-28): the protocol network as its own surface (`k7sd`, `qx5c`)

- Direction ruled by the owner: split the protocol's network
  infrastructure (`iroh-relay` + `cmd/lighthouse`) off the talos hub
  and off the cluster, into its own fly app; long term the hub is one
  consumer of the protocol among many, probably from another repo.
- Invariant 5 amended (scoped to the talos deployment, two dependency
  rules), goal line added, **ADR-0051 Proposed** — the amendment and
  the goal are ruled, the deployment shape is not reviewed.
- Relay admission spiked (`qx5c`): **protocol ADR-0012 Proposed, not
  reviewed** — the actor presents a `relay` chain to a gate in front
  of the relay; a newborn presents a wildcard link from its intro.
  No new verb, caveat or verifier rule.
- Three tests are the evidence; nothing else is built or deployed:
  `TestCrossRelayDial` and `TestRelayGate` (`iroh-transport`, real
  relay binary), `TestRelayAdmissionShapes` (`protocol/cert`).
  `irohtransport.Options` gained `RelayToken`.

## Loose threads

- ADR-0012 reverses, for the protocol relay only, the 2026-09-18
  ruling "relay is not a facet; no grant reaches the hook". The owner
  has not seen that yet.
- A relay bearer token cannot be replaced on a live iroh 1.1.0
  endpoint (protocol exploration log) — it carries a claim, never a
  credential.
- Open in ADR-0012: signing the id claim, a stranger door at the
  relay, onward delegation by a child, a per-client byte limit.
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

- Owner rules on ADR-0051's deployment shape and protocol ADR-0012.
- Then build in this order: the gate (verify + table + hook + proxy)
  as a package; the relay link in `protocol/spawn`'s intro and kit;
  presentation in `actors/cmd/child`; the fly app; move the
  provisioner and parent over; enforce the hub's member gate.
