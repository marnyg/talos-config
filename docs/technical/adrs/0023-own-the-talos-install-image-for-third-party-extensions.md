# ADR-0023: Own the Talos install image when a node carries a third-party extension

- Status: Accepted _(ruled 2026-09-16 at the Mesh v3 Phase 0 gate,
  decision `talos-config-b2t`, bead `5cz`; in effect on cp1)_
- Date: 2026-09-16
- Related: ADR-0011 (Longhorn — why the official extensions are
  there), ADR-0016 (identity-native mesh — the node agent is a Talos
  extension), `docs/mesh-v3-iroh.md` §P0.3 (the data),
  `talos/hardware/minipc.yaml`, `talos/extensions/p0agent/build.sh`

## Context and Problem Statement

Until now every node's install image was a **Talos Image Factory
schematic**: content-addressed, reproducible from a short YAML of
official extension names, hosted by Sidero. Mesh v3 puts our own agent
on the node as a system extension (P0.3), and the Image Factory only
accepts *official* extensions. P0.3 therefore built the installer with
`imager` and pushed it to `ghcr.io/marnyg/talos-installer`; cp1 has
booted it since 2026-09-15 while git still declared the factory image —
a knowing invariant-2 deviation that the gate had to resolve.

## Decision Drivers

- Invariant 2: git is the single source of truth — what cp1 boots must
  be what `talos/hardware/*.yaml` says.
- Mesh v3 Phase 1.3 grows the same extension on the same node; any
  interim baseline would be replaced within the phase.
- Supply chain: a factory schematic is pinned by construction; an image
  in a registry we control is pinned only if we say how.
- The node pulls its install image unauthenticated at upgrade time.

## Considered Options

### Option A: declare the imager-built image in git

Point `minipc.yaml` at `ghcr.io/marnyg/talos-installer:<talos>-p0agent-
<ver>@sha256:<digest>`; `talos/extensions/p0agent/build.sh` becomes the
documented way to produce it (every extension listed explicitly —
`--base-installer-image` does not inherit the factory's).

- Pros: git and node agree immediately; no upgrade churn; the chain
  Phase 1 needs anyway is adopted, not rebuilt.
- Cons: content-addressing is gone — a tag can be repointed, so the
  **digest is the pin** and must be updated on every push; a public
  image we build is now in the boot path (ghcr package must stay
  public; ghcr availability is a new dependency at upgrade time only).

### Option B: upgrade cp1 back to the factory schematic

One `talosctl upgrade` to `6a9acc…`, reintroduce the extension in
Phase 1.3.

- Pros: clean factory baseline for the gate record.
- Cons: ~10 min drain + reboot for a state Phase 1.3 undoes with the
  same imager chain; the agent's NodeId key survives on EPHEMERAL but
  the node is off the identity plane in between.

### Option C: get the extension into the Image Factory

- Ruled out: the factory carries `siderolabs/*` extensions only; there
  is no third-party submission path.

## Decision Outcome

Chosen: **Option A.** The factory cannot carry the extension, so B is
a round trip and C does not exist. The rule going forward: **a node
that carries a third-party extension declares an imager-built image
from our registry, pinned by digest.** Nodes without one (w1,
`alienware-x15.yaml`) stay on factory schematics.

### Consequences

- `build.sh` is deploy path, not spike scratch: push → update tag
  **and** digest in the hardware yaml → `talosctl upgrade`.
- The list of official extensions now lives in two places
  (`build.sh` and the factory schematic w1 uses) until w1 also carries
  the agent (Phase 1/2), after which the schematic comment is history.
- A Talos version bump means rebuilding and re-pinning the image, not
  editing a tag.

### Confirmation

`talosctl get extensions` on cp1 lists exactly the extensions
`build.sh` enumerates, and `minipc.yaml`'s digest equals the registry's
`Docker-Content-Digest` for the tag. Invalidated if the Image Factory
gains third-party extension support (then re-evaluate C) or if the
public-ghcr dependency proves unacceptable (then a registry on the hub).
