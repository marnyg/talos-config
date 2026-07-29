# Goals

<!-- The higher-order outcomes we're working toward. Merged "ideal state" + "higher-order goals".
     Each goal should be specific enough that you could tell whether you've achieved it.
     Time-scope goals if useful (this quarter / this year / 5-year vision). -->

The narrative north star lives in [`../vision.md`](../vision.md) ("Desired
end state" + "Explicit non-goals"). This file tracks the current goal set.

## Current goals

- **Blank metal → cluster member with one human act** (wallet signature).
  Everything else automatic, declarative, re-derivable from git + owner keys.
- **Mesh v2**: nebula replaces wg0 — direct peer paths (LAN traffic never
  hairpins through fly), phones/TV join the network, one overlay, one
  derivation tree. Spike gate passed 2026-07-29 (ADR-0002 Accepted);
  now in phase 1 (dual overlay). Full record in
  [`../mesh-v2-nebula.md`](../mesh-v2-nebula.md).
- **Provisioning plane stays minimal** — the Omni line in `vision.md`:
  no fleet management, no upgrade orchestration, no multi-cluster.
