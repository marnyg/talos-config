# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-11-28): the protocol network as its own surface (`k7sd`)

- Direction ruled by the owner: split the protocol's network
  infrastructure (`iroh-relay` + `cmd/lighthouse`) off the talos hub
  and off the cluster, into its own fly app; long term the hub is one
  consumer of the protocol among many, probably from another repo.
- Invariant 5 amended (scoped to the talos deployment, two dependency
  rules), goal line added, **ADR-0051 Proposed** — the amendment and
  the goal are ruled, the deployment shape is not reviewed.
- `TestCrossRelayDial` (`iroh-transport/transport_test.go`): endpoints
  homed on two different relays meet on a relay hint alone, with a
  negative control. Nothing is built or deployed beyond that test.

## Loose threads

- Relay admission for the new surface is open (`qx5c`): an open relay
  is free bandwidth for strangers.
- The caller connects to the foreign relay, so an enforcing hub gate
  blocks actor → member dials. No such edge exists today.
- `boot.ViaRelay` uses the actor's own `-relay` as the lighthouse dial
  hint; fine only while every actor shares the lighthouse's relay.
- The cross-relay test ran on loopback dev relays, not through fly's
  TLS proxy.
- **The working tree holds uncommitted Krea 2 / Mojo work**
  (`k8s/apps/comfyui/max-krea2/`, spike `j1kd`) from the session
  before; this session did not touch or commit it. Its state is in
  `max-krea2/README.md`.
- Carried over: ADR-0050 (any-port TCP) is still Proposed; the unused
  `longhorn-local` claims await the owner (`e6rx.8`).

## Suggested next steps

- Rule on ADR-0051's deployment shape, then answer admission (`qx5c`)
  before anything public is deployed.
- Build the app: image from `actors/image.nix` with `iroh-relay`
  added, a fly volume for the lighthouse key, deploy script, Gatus
  check; then move the provisioner and parent `-relay` and
  `-lighthouse` over and retire `k8s/apps/sap-lighthouse`.
- Stop `DefaultRelay` naming the hub.
