# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-07-31 — **Phase 2 steps 1 and 2 landed and verified** (task
1afafb50). Two deploy+unseal cycles, both budgeted.

- **Step 1, mesh certSANs** (`cf1de35`): `nebMachinePatch` now emits a
  two-document patch — a `machine.certSANs` merge (overlay IP +
  `<name>.mesh.internal`) ahead of the ExtensionServiceConfig — and
  `apiServer.certSANs` gained cp1's mesh identity. Verified live: apid
  and kube-apiserver certs carry `cp1.mesh.internal` + `10.42.218.125`;
  `talosctl -e 10.42.218.125` verifies TLS over the overlay; mesh DNS
  answers the name.
- **Step 2, endpoint cutover** (`6131602`): `cluster.controlPlane.endpoint`
  and cp1's `meta.yaml ip:` (the talosctl/apply dial target) moved to
  `10.42.218.125`; local talosconfig/kubeconfig re-pointed. Apply went
  through without reboot; apiserver refused connections ~30s and
  reconverged; node Ready, workloads healthy. The lease-drift port-scan
  dance is retired.

## Loose threads

- **Laptop can't resolve `.mesh.internal`** (task 04126746, +nice): the
  SAN is in the certs and the hub zone answers, but nothing split-DNSes
  the suffix to `10.42.0.1` locally — `talosctl -e cp1.mesh.internal`
  fails on resolution. IP endpoints everywhere meanwhile.
- **Cluster API now rides `ext-nebula`** starting on the node (the
  endpoint address lives on nebula0), where wg0 was a native Talos
  interface. Local-only dependency (tun comes up without the
  lighthouse); recovery via the LAN-address SANs is intact. Judged
  consistent with invariant 4 — noted, not silently absorbed.
- `argocd-dex-server` pod in Error is pre-existing (also in notes.md's
  absorbed-handover list), not endpoint-move fallout.

## Suggested next steps

- **Phase 2 step 3** — strip wg0 from compose, delete hub wg* code.
  Scoped during this session: it is *not* just deletion — the hub's
  `/config`-over-tunnel serving, ADR-0003's source-IP authentication,
  and auto-bootstrap dials all ride `wg.tnet` and must move to the
  nebula netstack (`nebstack.Listen` exists, unused for HTTP); main.go
  couples `--mesh-port` to `--wg-port` for unsealing; `wgup`/wg admin
  enrollment retire. Expect ADR-0003 to need superseding (mesh
  source-IP or cert-name as authentication).
- Closing invariant 5's dual-overlay exception is the payoff — if step
  3 stalls, that exception is the thing to question.
