# ADR-0044: App state is encrypted at rest and backed up as ciphertext to Tigris

- Status: Accepted 2026-10-20 (owner). Built the same day; first
  backup confirmation pending (see Confirmation).
- Date: 2026-10-20
- Related: ADR-0011 (Longhorn; "durability belongs on app state"),
  ADR-0031 (the library runs on one replica and never leaves the
  cluster), ADR-0004 (media is public), invariant 2 (data-plane state
  is a replication-and-backup problem; actor-owned state "encrypted
  to a durable key — storage may be hostile"), invariant 3 (Fly is
  trusted infrastructure, not a root of trust), invariant 8 (secrets
  plaintext only in memory); tasks `talos-config-9io` (encrypt the
  Longhorn volumes) and `talos-config-bsj` (backup target), decision
  `pp9h` (longhorn-user folded into the default class, earlier the
  same day).

## Context and Problem Statement

Longhorn has mirrored app state across two nodes since ADR-0011, and
that is all it did: no volume had a copy outside the cluster, so a
pruned PVC, a deleted Volume CR, a Longhorn upgrade gone wrong, or the
house, took the data with it. `bsj` ("prerequisite for a routine cp1
wipe") sat open from August. The question became concrete on
2026-10-20 when the owner asked whether w2's new NVMes should mirror
"the important volumes": they already were mirrored; what was missing
was a backup.

A Longhorn backup is a block copy of a snapshot. The volumes were
plaintext on disk (`9io`: the hub derives keys only for the system
disks), so a backup would have shipped plaintext — Forgejo repos, Home
Assistant tokens, the *arr API keys, the parent's session transcripts —
to a third party. Nothing today leaves disks the owner physically
holds; invariant 2's actor-owned-state rule and invariant 8's spirit
both say that copy must be ciphertext.

## Considered Options

1. **Longhorn → Tigris, plaintext, accept it** as inside the fly trust
   boundary (inv 3), with `9io` as a later fix. Fastest.
2. **Encrypt the volumes first (`9io`), then back up.** Longhorn's
   dm-crypt layer makes backups ciphertext for free; the cost is that
   a PVC's class is immutable, so every volume is a copy.
3. **A different tool** (k8up/restic, Velero+kopia) with client-side
   encryption under an owner-held passphrase. Meets the invariants on
   the existing volumes; file-level, not Longhorn-native, one more
   operator.

Option 1 was rejected by the owner once the exposure was spelled out.
Option 3 was not needed once option 2's cost was measured: ~85 GB
across 28 volumes, 24 of them under 5 GB — a per-app operation, not a
per-GB one, scriptable, two sessions. The first estimate of "weeks"
was wrong and withdrawn.

## Decision Outcome

**Option 2.**

- **`longhorn-encrypted`** (`k8s/apps/storage/storageclass.yaml`) is
  the cluster's default StorageClass: two replicas on different nodes'
  NVMe as before, plus `encrypted: "true"`. The CSI plugin opens each
  volume's LUKS header on the node with **one cluster-wide passphrase**
  in `longhorn-system/longhorn-crypto`. `Retain`. Its
  `recurringJobSelector` labels every volume it provisions into the
  `backup` group.
- **The passphrase** is generated once and lives in three derived
  places: the gitignored plaintext
  `talos/clusters/homelab/longhorn-crypto-secrets.yaml`; its `.age`
  (ssh key + the wallet-derived recipient, the same two the cluster
  secrets use — so the hub could serve it later, which is the
  direction `9io` wanted); and `k8s/apps/longhorn/crypto.sealed.yaml`,
  a kubeseal of the plaintext. A rebuilt cluster restores the
  sealed-secrets key from *its* `.age` (inlineManifests) and so unseals
  the same passphrase. Rotation is not a reseal — LUKS keyslots are per
  volume — so the value is treated as fixed.
- **The backup target** is a Tigris bucket on fly
  (`talos-longhorn-backup`, S3 API), set through the chart's
  `defaultBackupStore` with sealed credentials
  (`tigris-backup.sealed.yaml`). `backup-nightly`
  (`k8s/apps/storage/recurringjob.yaml`) runs `task: backup` on group
  `backup` at 03:45, retain 14. **Only ciphertext ever leaves the
  cluster**, and only app state: the bulk class (the library, caches)
  never joins the group; the plain `longhorn` class stays for bytes
  that want neither encryption nor a copy — VM ISOs, the win2k25 system
  disk (a from-git reinstall is 24 min), the kube-prometheus-stack and
  Loki StatefulSet volumes (metrics and logs; their templates are
  immutable too), Grafana's (dashboards are ConfigMaps in git). And,
  for now, **`gateway-state`**: the gateway fronts `cp1.mesh.internal`,
  the API server address the kubeconfig uses, so scaling it to 0
  severed kubectl mid-migration (2026-10-20, ~2 min outage, restored
  over the LAN with `--server=https://10.0.0.68:6443
  --tls-server-name=cp1.mesh.internal`, nothing changed). Moving it is
  a LAN-only run, pushing to the `github` remote (origin is Forgejo,
  also behind the gateway).
- **Migration** is `scripts/encrypt-pvc.sh`: pause the owning ArgoCD
  app, scale to 0, old PV to `Retain`, rsync once into a temp
  encrypted PVC, verify with an itemizing dry run, release both claims,
  recreate the original claim name bound to the encrypted PV, label
  the Longhorn volume into `backup`. Manifests pin `volumeName` from
  then on (ArgoCD's three-way diff otherwise tries to remove it, and a
  claim recreated without it would provision empty rather than rebind).
  `files/transfer` went first; 19 more followed the same day — **20
  volumes** encrypted and in the `backup` group. In PSS-`restricted`
  namespaces (`sap`) the copy runs as the workload's uid with busybox
  `cp -a` + `diff -r` instead of root rsync. Encrypted volumes have a
  64 Mi floor (Longhorn rejects <= 16 MiB; the LUKS2 header is 16 MiB),
  so two 16 Mi claims grew. Old PVs
  carry `encrypt-pvc/old=<date>` and are deleted once the first
  backups are verified.

- **Health and verification** (same day):
  - *Alerts* (`k8s/apps/monitoring/longhorn-backup-rules.yaml` → ntfy):
    target unavailable, a backup in Error, a backup-group volume with
    no backup newer than 30 h, the timestamp metrics missing, and the
    restore test not passing for 8 days. Longhorn's exporter has no
    backup timestamps, so kube-state-metrics' custom-resource metrics
    lift `lastBackupAt` (BackupVolume), creation time + group label
    (Volume) and `available` (BackupTarget) off the CRs.
  - *Restore test* (`k8s/apps/backup-verify`, Sundays 05:45): for each
    volume in the group, restore the newest backup from Tigris into a
    scratch encrypted volume, hash every file through the LUKS layer,
    delete it. Any miss fails the Job (`KubeJobFailed`). This exercises
    the whole recovery chain — bucket, credentials, per-block
    checksums, the passphrase, the filesystem — not just "an upload
    finished".
  - `restoreConcurrentLimit: 8`: restores are latency-bound to Tigris
    (2 parallel blocks ran ~6 MB/s).

### Consequences

- Fly/Tigris holds ciphertext under a key it never sees; the trust
  boundary of invariant 3 is unchanged. ~$2/month.
- A full restore needs two owner-held secrets: the sealed-secrets key
  (`.age`) and, through it, the passphrase. Lose the `.age` files and
  the bucket is noise — the same dependency the cluster secrets already
  have.
- First attach of a new encrypted volume costs ~90 s (LUKS format +
  argon2 keyslot); later attaches are seconds.
- `longhorn-csi-plugin` logs `CRYPTO_KEY_VALUE` verbatim in its
  `NodePublishVolume` line (1.12.0). Alloy drops that line before Loki;
  node-local container logs sit on the encrypted EPHEMERAL partition.
- `fly storage create` attaches a bucket to the org's only app and
  redeploys it: the hub restarted and went sealed; the five `AWS_*` fly
  secrets were unset the same minute (the hub holds no fly secrets).
  Next time: `-o personal` is not enough; expect the attach.
- ApplicationSet-generated apps re-apply their template, so a
  hand-paused child un-paused itself within seconds; the
  ApplicationSet now carries `ignoreApplicationDifferences` for
  `/spec/syncPolicy/automated`.
- `9io` is closed by this ADR for app state; the Longhorn *user
  volumes* themselves (the xfs under `/var/mnt/longhorn`) stay
  unencrypted — the plain class's bytes are the ones declared not to
  need it.

### Confirmation

- A throwaway encrypted PVC mounted on w2 via
  `/dev/mapper/pvc-…` (2026-10-20). BackupTarget `default` reports
  `available: true` against Tigris with the sealed credentials.
- `transfer`'s first manual backup completed (19 GB block copy of
  15.4 G of files, ~1 h at the house's ~4–5 MB/s uplink); BackupVolume
  recorded in the bucket. **Pending:** a restore of one backup
  into a fresh PVC on this cluster, and the first `backup-nightly` run
  covering every migrated volume.
