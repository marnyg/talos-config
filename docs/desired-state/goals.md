# Goals

<!-- The higher-order outcomes we're working toward. Merged "ideal state" + "higher-order goals".
     Each goal should be specific enough that you could tell whether you've achieved it.
     Time-scope goals if useful (this quarter / this year / 5-year vision). -->

The narrative north star lives in [`../vision.md`](../vision.md) ("Desired
end state" + "Explicit non-goals"). This file tracks the current goal set.

## Current goals

- **Blank metal → cluster member with one human act** (wallet signature).
  Everything else automatic, declarative, re-derivable from git + owner keys.
- **Mesh v2**: nebula replaces wg0 — **direct peer paths on the LAN**
  (LAN traffic never hairpins through fly), phones/TV join the network,
  one overlay, one derivation tree. Spike gate passed 2026-07-29
  (ADR-0002); **phase 2 complete 2026-07-30** — wg0 deleted, mesh is
  the sole overlay and control channel (ADR-0007). Remaining scope
  deliberately deferred: TV client (task 2e1bef85), remote-direct
  paths (ADR-0006). Full record in
  [`../mesh-v2-nebula.md`](../mesh-v2-nebula.md).
  _Remote_ peer paths are **not** a goal: measured 2026-07-30 as
  relay-by-default because ordinary remote networks (cellular CGNAT,
  corporate Wi-Fi) are symmetric NATs that no overlay can punch. Remote
  is wg0 parity via the hub; the LAN shortcut is the win. See ADR-0006.
- **Provisioning plane stays minimal** — the Omni line in `vision.md`:
  no fleet management, no upgrade orchestration, no multi-cluster.
- **Every exposed service authenticates against the wallet** —
  self-hosted SIWE SSO, no hosted identity anywhere in the access
  path. Substrate landed 2026-07-31 (ADR-0009: nebula-native ingress,
  tailscale gone); **reached 2026-07-31** — the in-cluster SIWE→OIDC
  bridge serves ArgoCD (native OIDC, dex deleted), the five media UIs
  (oauth2-proxy `auth_request`), and Jellyfin (jellyfin-plugin-sso).
  Remaining scope deliberately deferred: HTTPS over the mesh via the
  wallet-derived CA (task 75c8b6b3, `+later`).
