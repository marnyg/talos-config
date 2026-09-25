# Handoff — sovereign-actor protocol

<!-- "Where we left off" for the protocol scope. Overwritten per session.
     Deployment (talos/hub/fly) context lives in the root handoff. -->

## Last session

2026-09-25 (second session) — **M4.5 built: the child and provisioner
binaries, the child's beat, the image recipe** (`b93bfcc`). Nothing
under `protocol/` changed; all of it is in `actors/`.

- **`actors/child`** (C-free): `Run` = publish location → `spawn.Born`
  → beat. Each beat re-`#renew`s every held cert the parent issued
  (one request; each fresh cert replaces the old last link under
  every `(target, facet)` it sat at) and republishes. It returns
  `ErrLapsed` once no unexpired chain at `(parent, #renew)` remains —
  the self-lapse of invariant 13, on the child's side. A `#ping` echo
  facet is the P→C probe. Tested over `MemoryNetwork` against a real
  `Spawner` + `Provisioner`: birth, ping on the child's own consent,
  renew → `#extend` at the provisioner, lapse.
- **`cmd/child`**: env only (`SAP_INTRO`, `SAP_RELAY`, `SAP_BEAT`,
  `SAP_LOG`). Key minted in memory, never written. Homes at the first
  `iroh:relay=` in the parent's reach-me-at unless `SAP_RELAY` says
  otherwise. Exit 0 on lapse or SIGTERM.
- **`cmd/provisioner`**: `-driver k8s|docker`, `-state` (the key
  persists; `location.json` is derived, rewritten each beat),
  `-customer ID` (repeatable) → one consent over `#spawn/#extend/#kill`
  for `-customer-ttl` (365 d) minted at start — ADR-0009's open item,
  answered for v0 as "a flag". `Adopt` before `Listen`; `Sweep` +
  republish on `-beat`. Smoke-run against Docker Desktop.
- **nix**: `actors-bin` (host; runs the tagged suite `-race`),
  `actors-static` (musl), `actors-image` → `ghcr.io/marnyg/sap-actors`
  (`actors/image.nix`, child entrypoint, user 65534 for PSS
  `restricted`), `actors/build.sh` from the gateway's. `actors/` is now
  in the pre-push vendored list and CI's `vendor-hash` matrix.

## Loose threads

- **The image is not pushed yet.** `HUB_BUILDER=mar@nixos
  actors/build.sh` needs the linux box + GHCR token; `0bc.4.6` needs
  the printed digest.
- **How a customer finds the provisioner is a file.** `location.json`
  is the signed, expiring reach-me-at (10 min) copied out of band;
  after the first reply piggyback keeps it fresh. A lighthouse
  `#publish` (the hub runs none for actors yet) is the real answer.
- **Customer consent for a year, minted at start.** Revocation is
  expiry or a restart without the flag. Fine for the acceptance run;
  a market (M5) replaces the flag.
- **No k8s manifest for the provisioner** (Deployment + SA with Jobs
  RBAC + the state volume) — `0bc.4.6`'s, with the parent CLI.
- **The child's beat is wall-clock; certs are the actor clock.** In
  tests advance the clock between beats or nothing re-issues later
  than before (the byte-identical-cert note, 2026-09-23) — and
  `#extend` only fires when a re-issued exp passes the lease's current
  deadline, so a kit TTL below the birth window never extends on the
  first beat.
- Carried: registry auth not v0 (public image); `Running.Image` is as
  the platform names it; `DriverTimeout` bounds docker's pull;
  `payment` absent (M5); `fh2y`; open problems 8, 9; `cert`'s `rapid`
  suite ~6 min under `-race` (unfiled broken window).

## Suggested next steps

- Push the image; pin the digest.
- `0bc.4.6`: a parent CLI (`cmd/spawn`? laptop, loads
  `location.json`, `Spawn` by digest, logs birth / extend / lapse),
  the provisioner Deployment on the cluster and a `docker` run on a
  host, one child each; then promote ADR-0008/0009 to Accepted and
  prune exploration-log §M4.
