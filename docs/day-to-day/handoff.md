# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-07-30 — **Phase 2 step 3 landed, deployed, verified: wg0 is gone.
Mesh v2 phase 2 is complete** (task 1afafb50). Three commits, two
deploy+unseal cycles, one node apply.

- **Part 1** (`2fd66df`): control channel dual-homed onto the mesh —
  `serveMeshHTTP` (hello + admin-gated `/config` on the overlay,
  tcp/80), auto-bootstrap dials via the mesh netstack, `apply` fetches
  from `http://10.42.0.1`. Verified live before anything was deleted.
- **Part 2** (`c9c7360`, −2179 lines): unseal inverted into a new
  `hubManager` (master + age decrypt + KMS + mesh fan-out);
  `MESH_CA_PIN` replaces `WG_SERVER_PUBKEY`; all wg* code, `cmd/wgup`,
  `cmd/wgping`, udp/51820 deleted; `wgderive` → `masterderive` (HKDF
  info strings FROZEN — they still say "wg", renaming re-keys the
  fleet); new `cmd/recover` for offline break-glass; ADR-0007
  supersedes ADR-0003; invariant 5's dual-overlay exception closed.
- **Verified on cp1**: only `nebula0` link remains; apid SANs are LAN
  (10.0.0.31) + mesh identity only; node Ready after the expected ~30s
  apiserver blip; auto-bootstrap reports etcd-running over the mesh;
  `/sealed` = `hub: unsealed / mesh: up`.

## Loose threads

- **First `apply` after a hub redeploy can time out**: laptop→cp1
  tunnel needs lighthouse re-registration + handshake. A ping to the
  node's mesh address warms it; retry then succeeds. Candidate for a
  retry loop in the apply script if it recurs.
- `/sealed` now 503s on mesh startup failure (phase-2 inversion) — an
  external pinger pointed at it will page for mesh-down, not just
  sealed. Nothing is pointed at it yet.
- Laptop `.mesh.internal` split-DNS still missing (task 04126746) —
  bites slightly harder now that the mesh is the only overlay.
- `argocd-dex-server` Error pod: pre-existing, still unowned.

## Suggested next steps

- Close out phase 2 in the task tracker (task 1afafb50) and close the
  self-resolved items: 30 (wg0/service-CIDR overlap), 31 (wgderive
  divergence), 32 (dual-overlay hairpin).
- Push — main is ~14 commits ahead of origin.
- Pick next focus: laptop split-DNS (small), sealed-secrets upgrade
  (debt 4d6d9e26), or the EPHEMERAL media-disk thread (be79fbb1).
