# Handoff — sovereign-actor protocol

<!-- "Where we left off" for the protocol scope. Overwritten per session.
     Deployment (talos/hub/fly) context lives in the root handoff. -->

## Last session

2026-09-29 (second session) — **Lighthouse discovery for
provisioners (`0bc.6`): built, accepted on docker and on k8s, live in
the cluster.** The `location.json` handoff is gone from the path.

- **`Actor.Bootstrap map[ActorID][]string`** (`protocol/actor`): raw
  dial hints for an id with no live cached record — the network
  bundle's "lighthouse endpoints", invariant 11's sanctioned artifact.
  `Send` consults it only then; the first reply's piggyback supplies
  the record. `TestBootstrapHints` pins: a hint naming the wrong peer
  is `ErrPeerMismatch`, the right hint is used once, the cached record
  wins after. Rejected alternatives in ADR-0007 § Run live (transport
  fallback to the relay breaks `actor.Multi`'s `ErrUnreachable`
  contract; a long-TTL lighthouse file is still a file).
- **`actors/cmd/lighthouse -state DIR -member ID…`**: persisted key,
  per-member direct consents `publish #publish` + `invoke #lookup`
  (its own founder, `-customer`'s shape), beat re-publishes its own
  location and logs the directory size.
- **`provisioner -lighthouse ID`**: each beat `#publish`es the fresh
  record after writing `location.json` (kept: the no-lighthouse mode).
  **`spawn -lighthouse ID -provisioner-id ID`**: `#lookup`, validate,
  then `#spawn` unchanged; `-provisioner file` stays.
- **Docker acceptance:** first `#publish` on the relay hint alone was
  refused `unauthorized` (not yet a member) — the bootstrap dial works
  and the fold decides. After the member restart: `#publish ok` on
  every beat, directory 1; parent `#lookup ok → #spawn → born 0.9 s →
  #ping ok → 1 extend → lapse`. No file copied.
- **k8s acceptance:** image `47bae97@sha256:879d6580…` pushed;
  `k8s/apps/sap-lighthouse` up (id `ed:5cad808a…`, members: the
  provisioner + the laptop parent); provisioner restarted with
  `-lighthouse`, `#publish ok` on its first beat; laptop `spawn
  -lighthouse -provisioner-id` → `#lookup ok → #spawn → born 0.6 s →
  #ping ok`, Job running in `sap`. Nothing read off a pod. (Landing
  it needed ArgoCD's stuck sync op terminated twice — root handoff.)

## Loose threads

- The lighthouse's own `-member` list is a restart to change, as the
  provisioner's `-customer` is; the founder indirection (`-founder`,
  delegable consent to F who mints caps) is the way out when it itches.
- Child logs die with the Job (`ttlSecondsAfterFinished`) — carried.
- Carried: customer consent = a year-long flag; registry auth not v0;
  `payment` absent (M5); `fh2y`; open problems 8, 9; `bh74` shows on
  provisioners too; `lm2a` (lookup-cap in the intro) now has a
  lighthouse to point at.

## Suggested next steps

- Close `0bc.6` (owner's call). Then M5 money (`0bc.5`), or the
  `-print-id` / shared-preamble cleanups in `actors/cmd` (broken
  windows surfaced this session).
