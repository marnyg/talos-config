# Goals

<!-- The higher-order outcomes we're working toward. Merged "ideal state" + "higher-order goals".
     Each goal should be specific enough that you could tell whether you've achieved it.
     Time-scope goals if useful (this quarter / this year / 5-year vision). -->

The narrative north star lives in [`../vision.md`](../vision.md) ("Desired
end state" + "Explicit non-goals"). This file tracks the current goal set.

## Current goals

- **Blank metal → cluster member with one human act** (wallet signature).
  Everything else automatic, declarative, re-derivable from git + owner keys.
- ~~**Mesh v2**: nebula replaces wg0~~ — **history** (2026-07-29 →
  2026-09-21, ADR-0002/0007/0013; record in
  [`../mesh-v2-nebula.md`](../mesh-v2-nebula.md)). Superseded by Mesh
  v3 (ADR-0016); the last nebula code left the repo in Phase 4 P4.2.
  What carried over verbatim: _remote_ peer paths are **not** a goal —
  measured 2026-07-30 as relay-by-default because ordinary remote
  networks (cellular CGNAT, corporate Wi-Fi) are symmetric NATs no
  overlay can punch; the LAN shortcut is the win (ADR-0006).
- **Mesh v3**: identity-addressed mesh (iroh) replaces the nebula IP
  overlay — members are dialed by key, IP survives only as device-local
  fiction, k8s leaves the mesh onto declared LAN addresses, per-request
  device identity at the gateway. Direction committed 2026-09-03
  (ADR-0016; decision `talos-config-dlk`, trigger: sovereign-actor
  build-out); **Phase 0 gate passed 2026-09-16** (decision
  `talos-config-b2t`; relay, Android, Talos extension, API churn all
  probed); Phases 1–3 landed 2026-09-17 → 09-20 (dual plane, admin
  and media consumers migrated, soak on event coverage); **Phase 4
  P4.1/P4.2 done 2026-09-21** — no nebula extension, package, port or
  document anywhere; the identity plane is the only plane. Remaining
  before "reached": ADR promotions (P4.3) and this doc set (P4.4).
  Record in [`../mesh-v3-iroh.md`](../mesh-v3-iroh.md).
- **Sovereign-actor protocol at the center** (decision `talos-config-5w1`,
  2026-09-03): this repo becomes a monorepo around a reusable protocol
  — actors as keypair+wallet, authority as delegation certs,
  negotiation as signed proposals — with talos-config as its first
  consumer. v0 has no per-message money (PoW postage for strangers,
  invited children pay nothing); per-relationship flows settle on an
  EVM L2 (Base) chosen for wallet compatibility. Mesh v3 builds the
  protocol's transport, membership cert and gateway. Sketch:
  [`../../protocol/docs/sovereign-actor-protocol.md`](../../protocol/docs/sovereign-actor-protocol.md);
  restructure: `talos-config-k3o`.
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
