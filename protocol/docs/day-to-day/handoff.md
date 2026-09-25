# Handoff — sovereign-actor protocol

<!-- "Where we left off" for the protocol scope. Overwritten per session.
     Deployment (talos/hub/fly) context lives in the root handoff. -->

## Last session

2026-09-25 — **`udof` decided, M4.3 + M4.4 built: both drivers, in a
new `actors/` module** (`121f8e3`, `e316081`, `2b5c29d`).

- **Restart re-adopts, never persists** (decision `uzgl`; ADR-0009
  gained an amendment section; glossary updated). The lease table is
  a cache of what the driver rendered. `Driver` gained
  `List(ctx) []Running{Lease, Owner, Image, Handle}`; `StartSpec`
  gained `Owner`; every driver stamps `provisioner.LabelLease` /
  `LabelOwner` (`sap/lease`, `sap/owner`) — immutable facts only,
  never the deadline. `Provisioner.Adopt(ctx)` (call before Listen)
  takes each unheld one as `running` with `Until = now + AdoptGrace`
  (default 5 min); the owner's next `#extend` sets the real deadline,
  else `Sweep` kills it. Stateful provisioner ruled out (inv 12,
  second source of truth, first actor needing a durable key).
  `TestAdopt` pins it.
- **`actors/`** — own Go module (`replace ../protocol`), C-free, in
  CI's `go` matrix. Layout: `driver/` (shared `ParamsEnv =
  "SAP_INTRO"`), `driver/k8s`, `driver/docker`; `cmd/{provisioner,
  child}` to come. Both drivers are `net/http` against the platform
  API — four/five calls each, **no client-go, no docker SDK** —
  tested against in-memory fakes of those endpoints.
- **k8s** (`e316081`): Job `sap-<lease>` by digest, intro in env,
  `activeDeadlineSeconds = until − startTime`, PSS `restricted`
  contexts, no SA token, `ttlSecondsAfterFinished 600`. **Verified
  live on k8s 1.32: `activeDeadlineSeconds` is mutable both ways and
  fires on shortening** — Extend is native, Sweep is bookkeeping.
  An actor id is not a legal label value (`:`; 67 chars), so
  `sap/owner` is an **annotation** on k8s. `InCluster()` reads the SA
  mount; no kubeconfig support.
- **docker** (`2b5c29d`): create named `sap-<lease>` with labels +
  env + `AutoRemove`; pull by digest on "no such image" (JSON-line
  stream, inline errors); Extend no-op; Kill `rm -f` (404 = done);
  List by label + `status=running`. Verified live against Engine API
  1.54 (`SAP_DOCKER_LIVE=1 go test -run TestLive`).

## Loose threads

- **Registry auth is not v0** on either driver: the child image must
  be public or pre-present. The hub recipe publishes where?
  (`0bc.4.5` decides.)
- **`Running.Image` is as the platform names it** — a containerd-
  store daemon normalises to `docker.io/library/…@sha256:…`. Only
  informational on adopt; do not compare names, compare digests.
- `Provisioner.DriverTimeout` (60 s) bounds docker's `Start`, which
  pulls inside it. Fine for a small image; a goroutine per Start if
  it hurts (ADR-0009 consequence).
- The pre-push hook's vendored-tree list does not yet name `actors/`;
  it must once `cmd/provisioner` gets a nix build and a vendorHash.
- Carried: `payment` absent (M5); `fh2y`; the provisioner's consent to
  a customer is one root over three facets (ADR-0009 open item);
  open problems 8, 9. `cert`'s `rapid` law suite takes ~6 min under
  `-race` (broken window, unfiled).

## Suggested next steps

- `0bc.4.5` `cmd/child` + `cmd/provisioner` + image: read
  `driver.ParamsEnv`, mint a key, `spawn.Born`, renew on a short beat,
  exit on no live edge; provisioner binary = `provisioner.New` +
  `Adopt` + a driver picked by flag; linux cgo iroh image via the hub
  recipe (`fly/image.nix`). Read `spawn.Born` and `fly/image.nix`
  first. Add `actors/` to the pre-push vendored list then.
- `0bc.4.6` acceptance on both platforms; promote ADR-0008/0009;
  prune exploration-log §M4.
