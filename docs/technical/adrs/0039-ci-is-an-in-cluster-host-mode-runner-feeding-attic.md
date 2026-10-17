# ADR-0039: CI is one in-cluster, host-mode Forgejo runner whose unsandboxed builds feed Attic

- Status: Proposed
- Date: 2026-10-17
- Related: ADR-0035 (kata for agent workloads — not used here),
  ADR-0011 (longhorn-bulk = re-derivable), ADR-0029 (Longhorn classes),
  invariant 2 (data-plane exception), invariant 8 (plaintext only in
  memory); decision `talos-config-4la8` (talos-config stays on
  GitHub); spike `talos-config-kyiy` (the caches); task
  `talos-config-0m50` (this build-out); chore `talos-config-1ova`

## Context and Problem Statement

The cluster has a binary cache (Attic, `cache.gw.mesh.internal/main`)
and a pull-through mirror of cache.nixos.org, but nothing filled Attic
except a laptop's post-build-hook. Every x86_64 machine rebuilt the
repo's iroh-ffi, config-server and images, and the NixOS box rebuilt
its own system. The owner asked for CI on the cluster that builds this
repo's flake and the NixOS systems of `github:marnyg/nixos`, and
pushes the results to Attic.

A nix build wants a sandbox (user namespaces), a writable store, and
build users. The cluster defaults to PSS restricted. Whatever runs the
builds also signs, through Attic, paths that every machine trusts by
`trusted-public-keys`. So the question is which executor, with which
privilege, can run those builds, and what the trust in its outputs
then rests on.

## Decision Drivers

- No privileged pods, no host access (PSS baseline at most).
- Everything declarative in this repo; the runner's credentials are
  sealed secrets, its Forgejo registration is re-derivable on start.
- talos-config stays on GitHub (`4la8`): CI must work off a mirror.
- Invariant 8: credential plaintext only in memory.
- Fast warm runs; a cold run may be slow.
- What reaches the cache is the owner-signed `main` of this repo and
  the owner's NixOS repo, not code from strangers.

## Considered Options

### Option A: forgejo-runner with docker-in-docker

The standard Forgejo setup: the runner spawns a container per job.

- Pros: per-job isolation; `uses:` actions work.
- Cons: dind needs a privileged pod; nix inside the job container
  still has no sandbox without more privilege. Pays for isolation it
  does not get.

### Option B: runner on the NixOS box (`services.gitea-actions-runner`)

- Pros: a real multi-user nix with sandbox and a persistent store;
  pushes through the mesh gate with no Attic token at all.
- Cons: its config lives in another repo; the box is a workstation,
  not infrastructure; the cluster would depend on it for CI.

### Option C: host-mode runner in the cluster under kata

- Pros: own guest kernel, like the agents (ADR-0035).
- Cons: the store over virtio-fs crawls (`5h0j`); a nix build is the
  most metadata-heavy workload there is.

### Option D: host-mode runner in the cluster, PSS baseline, nixbld users, no sandbox

One forgejo-runner Deployment (label `nix:host`): job steps are
processes in the runner's own container. nix runs as root without a
daemon; builds drop into `nixbld1..32` build users; `sandbox = false`.
The pod drops all capabilities and adds back CHOWN, DAC_OVERRIDE,
FOWNER, SETUID, SETGID, KILL (all in baseline's default set). No
volume: the store is the container overlay, warmed from the mirror and
Attic by Service name, and kept until the pod restarts.

- Pros: no privilege beyond baseline; everything in this repo; warm
  runs take about a minute; builds cannot write the store or `/`.
- Cons: no sandbox, so a build sees the network and the whole store
  and can be impure without nix noticing; one shared runner, so jobs
  are not isolated from one another; no JavaScript actions (`uses:`).

## Decision Outcome

Chosen: **Option D**, because it is the only option that is fully
declarative here and needs nothing past PSS baseline. The isolation A
and C would buy is either not delivered (A) or too slow to use (C).
Option B stays the fallback if CI ever needs a real sandbox.

How it fits together (`k8s/apps/forgejo-runner/`,
`.forgejo/workflows/nix-cache.yml`):

- **Trigger:** Forgejo pull-mirrors GitHub into `mirrors/talos-config`
  every 10 min. A mirror sync is a `push` event, so the workflow runs
  on every push to `main`, plus nightly and on dispatch.
- **Fetch:** nix fetches the mirror by Service URL with the run's own
  token. A credential helper scoped to that URL is passed through
  `GIT_CONFIG_*`. The `mirrors` org is not public.
- **Build:** every x86_64-linux package of this flake plus the
  desktop, laptop and wsl toplevels of `github:marnyg/nixos`, one
  target at a time, so one red build hides nothing.
- **Push:** `attic push` (token `sub ci`, pull+push on `main`; Attic's
  upstream filter skips cache.nixos.org paths) to the attic Service.
  The pod pins `cache.gw.mesh.internal` to that Service's pinned
  ClusterIP, because the client follows the advertised api-endpoint.
- **Credentials:** the runner's pre-shared secret and the Attic token
  are sealed. The entrypoint writes them mode 600 onto memory-backed
  emptyDirs: invariant 8, and unreadable by the build users.
- **Attic:** chunking is off (`nar-size-threshold = 0`). Per-chunk
  sqlite writes on longhorn-bulk timed out the pool on the first full
  push; cross-NAR dedup was 1.6%.

### Consequences

- Machines that follow `main` substitute instead of building: iroh-ffi,
  config-server, the images, and the NixOS systems.
- The trust in `main:` widens. A path signed by Attic now means either
  "a member device pushed it" or "an unsandboxed build of an
  owner-signed rev produced it". It does not mean "a sandboxed,
  reproducible build produced it". A flake input that misbehaves at
  build time could poison the cache. That is accepted at N=1, since
  only the owner's two repos and their pinned inputs ever build here.
- The darwin outputs (the Mac) are still not built by anyone.
- The first run on a fresh pod is cold, about 1.5 h on w1.
- Pushing an image to ghcr stays a laptop act; the runner only caches.

### Confirmation

Live 2026-10-17: run #9 green. `nix path-info --store
http://cache.gw.mesh.internal/main` from the Mac hits
`config-server-0.1.0` and all three `nixos-system-*` toplevels.

Revisit if: a build needs the sandbox to be correct (impurity bites);
anything but the owner's repos is given the runner; or Option B's
box becomes infrastructure anyway.
