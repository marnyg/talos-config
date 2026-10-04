# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-04 (evening): **RDP to win2k25 over mesh v3** (`j5c5`, ADR-0017
amendment 2026-10-04). The NodePort 30389 had been dead since the
nebula render went (`irohup`: "port 30389 is not a node facet");
Jellyfin's 30096 is in the same state. Added the `rdp` gateway facet
(policy.go + Nickel + glossary), `-rdp` forward on the gateway
(image `a72f4ce` live), `{facet: rdp, group: admins}`, Service →
ClusterIP; hub redeployed `5059794` and unsealed; desktop's irohup
rebuilt. `xfreerdp /v:rdp.gw.mesh.internal` works — pass `/p:` from
secret `win2k25-admin` (lockout at 10 bad tries) and a fixed
`/size:`; `/dynamic-resolution` under a tiling WM leaves the lock
screen half-painted. Guest ops without a guest agent: WinRM 5985 via
`kubectl -n vms port-forward pod/<virt-launcher>` + pywinrm (NTLM);
anything touching the user's vault/session (cmdkey, net use) must
run as a `LogonType Interactive` scheduled task — that is how `Z:`
→ `\\samba.files.svc.cluster.local\transfer` (as `mar`, persistent)
was mapped. Filed `f17b` (Windows agent build + provisioning-time
enrollment, under `dsuj`); noted on `dsuj` that the VM has no GPU
(bochs, no hostDevices, permittedHostDevices unset). Also cut
Jellyfin's NodePort 30096 (`0a60ec0`) — same dead door; no web
NodePorts remain. Open threads not filed: `irohup -bridge` accepts
node facets only (gateway facets unreachable in bridge mode,
`cmd/irohup/main.go:128`); `docs/mesh-v3-p0.2-android.md` still
describes Jellyfin over :30096.

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

- The `files` share **stays running by decision** (2026-10-05): it is
  mapped as `T:` on the Windows PC (`e6yj`, closed) and the PC is
  still in use. Password auth on the LAN remains the stated exception
  to "every exposed service authenticates against the wallet"; the
  `replicas: 0` one-liner in `k8s/apps/files/deployment.yaml` is the
  off switch when that changes.
- `ch74`'s real question (share vs sync-flow for machine↔machine
  files) is untouched. The data-class wording for the transfer dump
  is now in `domain-model.md` ("user files", a knowing mismatch with
  the bulk tier); `9io` (encrypt user volumes) is the related thread.
- `vzbf` (gateway WebSocket panic) and `lwi3` (sillytavern) are
  closed; `dsuj` is now blocked only by the data transfer off the
  Windows PC.

## Suggested next steps

- Copy the data off the Windows PC; then `dsuj` (Windows PC as a
  node) and `f17b` (Windows agent) become unblocked.
- `hwtp` (seerr) remains the smallest open app item.
