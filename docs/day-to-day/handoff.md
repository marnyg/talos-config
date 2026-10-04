# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-04 (later): **SMB transfer share live on nas1's bulk tier**
(`e6yj`, first concrete answer to spike `ch74`), plus a latent
storage-class bug found on the way.

- **`k8s/apps/files/`** — namespace `files` (PSS privileged, for
  `hostPort 445`: the Windows SMB client dials 445 only), PVC
  `files/transfer` 100Gi RWO on `longhorn-bulk`, samba 4.23.8
  (`servercontainers/samba` smbd-only) pinned to nas1, account `mar`
  in a SealedSecret. Two doors: `\\10.0.0.74\transfer` from the LAN
  (the Windows PC), `\\samba.files.svc.cluster.local\transfer` for
  KubeVirt guests (masquerade → ClusterIP). Verified with smbclient
  put/ls/del. Password auth on the LAN is a stated, temporary
  exception to "every exposed service authenticates against the
  wallet" — scale to 0 after the transfer; the PVC stays (Retain).
- **`longhorn-bulk` could never provision a new volume**:
  `replicaSoftAntiAffinity: "true"` / `replicaDiskSoftAntiAffinity:
  "false"` are not Longhorn values (`enabled`/`disabled`/`ignored`);
  the three media volumes had been patched by hand so the class was
  never exercised. Fixed in git, class deleted and recreated by ArgoCD
  (`k8s/apps/storage/storageclass.yaml`).
- **ArgoCD deadlock pattern seen**: a running sync was waiting on the
  pending PVC, which was waiting on the StorageClass the same sync had
  not re-applied. Removing `.operation` did nothing; patching
  `.status.operationState.phase=Terminating` (what `argocd app
  terminate-op` does) freed it. Then the CSI provisioner's backoff
  outlived patience — deleting and re-applying the PVC was faster.

## Loose threads

- The `files` share is **on until the owner turns it off**: set
  `replicas: 0` in `k8s/apps/files/deployment.yaml` once the Windows
  PC is emptied. `e6yj` stays `in_progress` until then.
- `ch74`'s real question (share vs sync-flow for machine↔machine
  files; what a "user file dump" is worth — it is neither app state
  nor re-downloadable library, yet sits on the disposable bulk tier)
  is untouched. Related to `9io` (encrypt user volumes).
- `vzbf` (gateway WebSocket panic) still `in_progress`; close after a
  clean day.
- `dsuj` is blocked by `lwi3` (sillytavern, deployed last session —
  confirm the Windows copy can be retired) and now, practically, by
  the data transfer this share exists for.

## Suggested next steps

- Copy the data off the Windows PC; then `lwi3` can close and `dsuj`
  (Windows PC as a node) becomes unblocked.
- `hwtp` (seerr) remains the smallest open app item.
- Decide the domain-model wording for the transfer share's data class
  (proposed, not written — see `domain-model.md` "Storage tier / data
  class": `longhorn-bulk` is described as RWX, but the class is not;
  the library's claims are).
