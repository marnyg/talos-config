# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-07-30 — **QoL/debt burndown: four tasks closed** (04126746,
4d6d9e26, 85ba4de5, 544ef6be), four commits pushed, hub deployed +
unsealed.

- `9108321` — nebup split DNS: `.mesh.internal` routed to the hub's
  overlay resolver via resolvectl, run-path only, per-link config dies
  with the TUN. `meshDNSZone` moved to `nebderive.DNSZone`. Verified
  live (`cp1.mesh.internal` resolves on link nebula1).
- `daf2847` — sealed-secrets controller re-vendored 0.27.3 → 0.38.4
  (old file verified byte-identical to upstream, so no local patches).
  ArgoCD synced; both sealing keys registered; all three SealedSecrets
  re-unsealed.
- `29df1d7` — nebup warns when a VPN/exit-node link carries the route
  to the hub (the punch-measurement poisoner). Warn, not refuse.
- `bb24696` — status.go: shared `walletSignJS` + `statusPageHead`
  dedupe login/dashboard templates. Deployed to the hub.

Post-deploy: hub unsealed at `/status`, mesh up, cp1 direct LAN path
(1.2ms) restored after the expected ~60s lighthouse re-registration.

## Loose threads

- Cached `~/.config/talos-mesh/laptop.yml` predates phase 2 (retired
  wg0 underlay-filter entry, hand-set `tun.dev: nebula1`) — harmless;
  `nebup -reenroll` refreshes it.
- `-mesh-dns-zone` flag is configurable on the hub but nebup hardcodes
  `nebderive.DNSZone` — a custom zone would serve names nebup doesn't
  route. Noted, not filed.
- `/sealed` external pinger, `argocd-dex-server` Error pod: unchanged,
  still unowned.

## Suggested next steps

- EPHEMERAL media-disk thread (task be79fbb1) — the last item from
  this session's list; needs a design decision before code.
- Or: phone onboarding UX (5183f6ea), TV client (2e1bef85) — both
  deliberately deferred, mesh is otherwise in dogfood mode.
