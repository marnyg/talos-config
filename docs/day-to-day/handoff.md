# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-10-18, observability wrap-up)

**The observability epic is recorded and alerting is wired** (`xhk3`
→ `drou`, `88uq`):

- ADR-0041 (`drou`): the stack is a data-plane tenant; Grafana's login
  is the admins gate, `X-Mesh-Name` is only the username. Recorded
  after the build (live since 2026-10-16).
- ADR-0042 (`88uq`): pages go to a **self-hosted ntfy** over the mesh
  — chosen over ntfy.sh/Telegram/email because a sealed hub still
  relays (`relay.go`), so the sealed-hub page is deliverable in-mesh
  without a hosted account. New `k8s/apps/ntfy` (1Gi Longhorn cache,
  `ntfy.gw.mesh.internal`, no auth, no upstream/Firebase) + app-of-apps
  line; Gatus `alerting.ntfy` with every endpoint on `alerts:
  [{type: ntfy}]` (3 misses → page, 2 hits → all-clear, hub row
  priority 5); Alertmanager → `ntfy-alertmanager` shim
  (`k8s/apps/monitoring/ntfy-alertmanager.yaml`), Watchdog/
  InfoInhibitor to null, upstream inhibit rules.

Deployed by the push that carries this file; not yet observed live —
see next steps.

## Loose threads

- **ADR-0042 confirmation is live-only:** nothing has paged yet. The
  phone needs the ntfy app (Play or F-Droid) pointed at
  `https://ntfy.gw.mesh.internal` (or `http://` if the app will not
  use the meshtls user CA), topic `alerts`, DND override on for
  priority 5. Then scale a probed Deployment to 0 for the Gatus path,
  and a deliberate `KubePodCrashLooping` for the Alertmanager path.
- Default chart rules on Talos may be noisy once they can page
  (kube-proxy/etcd targets, `TargetDown` on anything the Operator
  cannot reach); tune `defaultRules.rules` or add a `null` route per
  alert rather than silencing in Grafana.
- `k8s/apps/monitoring/ingress.yaml` and `gatus/ingress.yaml` comments
  still describe a per-Ingress auth-url annotation / "group= query" that
  moved to the controller's global-auth-url (`332b463`).
- Carried from 2026-10-17/18: `d0o0` in progress (durable
  `talos/pins/<address>.json` copy); the sandbox-agent outfit digest is
  pinned by hand in two places (`b35t`); the hub re-seals on every
  deploy — sign at `/status`; workouts offline queue unverified live
  (`tmm5`); `authorize.qnt` depth-2 over budget (`xrfm`).

## Suggested next steps

- Push, let ArgoCD sync `ntfy` + `monitoring` + `gatus`, watch
  `kubectl -n ntfy get pods` and `kubectl -n monitoring logs deploy/
  ntfy-alertmanager`; then the phone subscription and the two live
  tests above; close `88uq` on the first real page.
- Back to the line in focus.md: the parent actor (SQLite+FTS5 store,
  list/watch/steer UI behind the person gate).
- `qx5c`: relay admission for sovereign actors before the gate is
  enforced again.
