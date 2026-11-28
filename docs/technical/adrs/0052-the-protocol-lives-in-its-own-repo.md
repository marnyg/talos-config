# ADR-0052: The protocol lives in its own repo; talos-config is one consumer

- Status: Accepted 2026-11-28 (owner, the seven decisions of spike
  `talos-config-d2up`). Done the same day: github.com/marnyg/sap at
  `{protocol,iroh-go,iroh-transport,actors}/v0.1.0`; talos-config
  consumes it (`55818ee`).
- Date: 2026-11-28
- Supersedes: ADR-0020 (the repo is a monorepo around the protocol)
- Related: ADR-0051 (the protocol network is its own public surface —
  the deployment split that proved the boundary), protocol ADR-0012,
  goal "the hub is one consumer among many".

## Context and Problem Statement

ADR-0020 put the sovereign-actor protocol at the center of this repo
with talos-config as its first consumer, and rejected a separate repo
while the protocol had no second consumer and its boundary was
unproven. By 2026-11-28 both had changed: the protocol network runs on
its own fly app with its own relay and lighthouse (ADR-0051), the hub's
relay no longer carries protocol traffic, and the owner's direction is
that agents use the protocol with no talos anywhere — the hub one
consumer among many, "probably from a completely different repo/app
context". Inside one repo the dependency direction was already clean
(nothing in `protocol/`, `iroh-go/`, `iroh-transport/` or `actors/`
imported talos code), but the coupling ran through shared tooling:
relative `replace`s, one flake, one CI, one beads database.

## Decision Drivers

- The goal: the protocol runnable with no talos code or infrastructure.
- Protocol invariants are deployment-independent; a repo whose root is
  a homelab is a standing temptation to break that.
- The hub must not be able to change the protocol in passing: a
  protocol change it needs should be a release it adopts.
- Nothing the hub runs may change behaviour by the move.

## Considered Options

- **A — stay a monorepo (ADR-0020).** No migration; the boundary stays
  a convention.
- **B — separate repo, consumer by Go module tag + flake input.**
- **C — separate repo vendored into talos as a git submodule/subtree.**
  Keeps relative `replace`s working but keeps the coupling ADR-0020
  had, now with submodule friction.

## Decision Outcome

**B**, with the owner's seven rulings:

1. Repo `marnyg/sap`; modules `github.com/marnyg/sap/{protocol,iroh-go,
   iroh-transport,actors}` — four, so `protocol` stays C-free.
2. Moved: the four modules (including the parent, `cmd/sap`, the k8s
   and docker drivers), `actors/net` (the network's fly app), the image
   builds (`sap-actors`, `sandbox-agent`, `marnyg-sap-net`), the
   protocol's Quint models (authorize, clock, rotation), and
   `protocol/docs` as that repo's `docs/`. Stayed: `config-server`, the
   talos Quint models, and the k8s manifests that pin sap's images.
3. talos-config requires the modules by tag (`<module>/vX.Y.Z`, all
   four released together) and takes the sap flake as an input
   (`nixpkgs` follows ours) for `iroh-ffi-static`(`-musl`) and
   `iroh-relay`(`-static`). Cross-repo work: a gitignored `go.work`.
4. History kept with `git filter-repo` (281 commits); rewritten
   commits lose their signatures, which only this repo's hub checks.
5. Hosting: Forgejo origin + public GitHub (the Go proxy needs public).
6. Beads: sap has its own database; the 18 open protocol issues were
   recreated there with back-references and closed here as moved.
7. This ADR; the protocol keeps its own ADR numbering.

### Consequences

- Good: the boundary is enforced by the toolchain — talos code cannot
  reach an unexported protocol detail or change the protocol without a
  release.
- Good: the hub's build is byte-identical in its native parts (sap's
  `iroh-ffi-static` and `iroh-relay-static` are the same store paths).
- Bad: a protocol change the hub needs is two commits and a tag — sap,
  then a version bump here (+ `vendorHash`). `go.work` softens it
  locally.
- Bad: two flakes, two CIs, two beads databases; an issue that spans
  both is filed where the work lands and cross-referenced.
- Hosting as ruled: `mar/sap` on Forgejo, created through the cluster
  with `k8s/apps/forgejo/migrate-from-github.sh sap` (sysadmin from its
  sealed secret, push mirror to GitHub with the stored mirror PAT, sync
  on commit). sap's beads live on Forgejo's `refs/dolt/data`; the
  mirror prunes GitHub's copy, the same known gap as `c19b` here.

### Confirmation

- sap builds and tests standalone: `protocol-tests`, `iroh-go-smoke`,
  `iroh-transport`, `actors-bin`, Quint `run`.
- talos-config builds with no protocol source in the tree:
  `config-server-bin` (tagged suite), `hub-image`, `gateway-image`,
  `scripts/test-iroh.sh`, the three CGO-free `Dockerfile.cmd` binaries.
- Wrong if: the hub starts needing sap releases faster than the
  protocol stabilises — then the friction says the boundary is drawn
  in the wrong place, not that it should be erased.
