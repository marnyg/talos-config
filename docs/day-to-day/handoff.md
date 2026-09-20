# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-20 (seventeenth session, late night) — **Phase 4 started: the
nebula extension is off both nodes (`359.11.1` closed).**

- `99299ae` — P4.2 prerequisite: the `<name>.mesh.internal` apid
  certSAN moved from the nebula render (`mesh.MachinePatch`) to the
  identity-plane render (`agentPatch` → `machineSAN`, `nodeenroll.go`);
  the overlay-address SAN is gone; `bootstrap.zone()` reads
  `fakeip.Zone`. `nebmachine.go` now emits only the nebula
  ExtensionServiceConfig and has no load-bearing role.
- `14c84db` — P4.1: `talos/extensions/installer.env` without
  `nebula:1.10.3`; fleet image `v1.12.6-p0agent-0.1.5@sha256:3c2c7cc3…`
  (agent at `2c2f607`, the h2-liveness client) pinned in both
  `talos/hardware/*.yaml`; `cluster.yaml` loses the `10.42.218.125`
  apiserver SAN and `nodeport-addresses: 0.0.0.0/0`.
- Nodes: w1 upgraded 21:27Z (~3.5 min), cp1 21:34Z (~7 min drain).
  Extensions now `iscsi-tools, util-linux-tools, p0agent 0.1.5`; no
  `nebula0`; same NodeIds; `beat ok` on both. Hub redeployed 21:42Z
  (bakes `talos/`), unsealed, `nix run .#apply` to both nodes 21:43Z
  without reboot; kube-apiserver cert regenerated without `10.42.x`.
- `hyjv` closed: w1's member addresses are `[10.0.0.71]` only,
  `talosctl -n w1 -e cp1` answers, so `apply`'s `-n <hostname>` works.

## Loose threads

- The hub still runs its nebula lighthouse (fly udp/4242, `--mesh-port`)
  and still serves a `nebula` ExtensionServiceConfig document that no
  node has a service for (Talos accepted the apply; inert). Both die
  with P4.2.
- `talos/mesh-policy.yaml` keeps the dead `host: hub` row until the
  file dies with the render (noted on `359.11.2`).
- `mesh.MachineDNSName` needs a home outside `mesh/` before the
  package goes (`nodeenroll.go`, `bootstrap.go`, `status.go` use it);
  `status.go:731` reads the DNS zone off the nebula manager —
  switch to `machineSAN`.
- Longhorn `pvc-1a3572dc…` was `degraded` (replica rebuild after the
  two reboots) at session end; two kubevirt `Error` pods are drain
  leftovers with Running replacements.
- `c4vd` (w1's machine dir is the dock's MAC) is untouched by upgrades
  — only a reinstall re-fetches by MAC.
- TV admin session; Mac daemon binary age unknown (`darwin-rebuild
  switch` pending since the previous session).

## Suggested next steps

- **P4.2** (`bd show talos-config-359.11.2`): delete
  `config-server/mesh/neb*.go`, `nebderive`, `nebstack`, `nebenroll.go`,
  `cmd/nebup`, the vendored nebula service pkg, nebula parts of
  `android/build-aar.sh`, the DNS shim in `mobile/`; drop `--mesh-*`
  flags and `MESH_CA_PIN` from `fly.toml`; then a hub redeploy +
  unseal (one signature) and a phone APK without the nebula AAR.
  Read the notes on the bead first — they carry the must-carry list.
- P4.3 ADRs and P4.4 docs (`359.11.3`, `359.11.4`) after; the
  `deployed-state.md` rewrite belongs to P4.4.
