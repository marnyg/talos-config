# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-08-11→14 (eighth session, several sittings) — **Windows guest arc,
verified and closed.** KubeVirt + CDI operators landed
(`k8s/apps/{kubevirt,cdi}/`); Windows Server 2025 guest on cp1
(`k8s/apps/vms/`), RDP via NodePort 30389 — owner RDP-verified
2026-08-14. Reinstall is one API act: sealed autounattend + no-prompt
ISO repack Job (7z extracts the UDF layer, xorriso rebuilds with
`efisys_noprompt.bin`) + a suspended-CronJob trigger with two-DELETE
RBAC. Exercised end-to-end 2026-08-14 (trigger Job Complete, system DV
re-imported, RDP back). Sync-waves + a CDI DataVolume health
customization close the cold-start boot race (`f29dbf2`). Also
confirmed: **ArgoCD wallet login works in the browser** (`fed04b4`
verified end-to-end — that loose thread is closed).

## Loose threads

- **w1 still down** (since 2026-08-04) — 3 `longhorn-bulk` volumes
  `faulted` (media library offline); the VM's system/ISO volumes run
  `degraded` 1-of-2 replicas and heal when w1 returns. Old jellyfin
  pod still admin/admin until it cycles (new password:
  `kubectl -n media get secret jellyfin-admin …` after w1 returns).
- **ADR-0012 still Proposed, not Accepted**; enrollment implementation
  not started. Exploration-log section stays until accepted.
- **virtio switch deferred**: guest runs SATA + e1000e deliberately
  (inbox drivers); viostor/NetKVM + Block-mode disk is a later perf
  step, not owed now.
- Rotating the win2k25 admin password means resealing **both**
  `win2k25-admin` and the autounattend secret — nothing links them.
- Phone enrollment accepted-broken until the APK exists (unchanged).

## Suggested next steps

- Power-cycle w1; verify `longhorn-bulk` volumes leave `faulted`, VM
  volumes heal to 2 replicas, jellyfin's new pod starts.
- Accept ADR-0012 (flip status) and start the hub-side verify+mint
  core.
- Decide whether the KubeVirt/Windows-guest choice gets an ADR (was
  prompted at close of this session).
