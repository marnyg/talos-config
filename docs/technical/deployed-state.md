# Deployed state

Where the running system stood when last verified. Facts here decay —
each block carries the date it was last confirmed. If you verify or
change something, update the date.

**Rewritten 2026-09-21 (Mesh v3 Phase 4, `359.11.4`)** against the
live system over the identity plane. Nothing nebula-era runs or exists
in the tree; the previous revision of this file (nebula lighthouse,
`10.42/16`, `MESH_CA_PIN`, phase-1 punch/iperf measurements) is in git
history and, where the numbers still matter, in ADR-0006 and
`docs/mesh-v2-nebula.md`.

## Summary — _verified 2026-09-22_

- **One plane.** Members (cp1, w1, nas1, the in-cluster gateway, the
  owner's Mac, phone) are dialed by Ed25519 NodeId over iroh/QUIC; the
  hub on fly is the relay and the issuer; per-request identity reaches
  apps as a header from the gateway. IP survives only as each device's
  own fake-IP zone (`198.18/15`, `*.mesh.internal`).
- **Three nodes since 2026-09-22**: nas1 (storage) joined; hub runs
  `c33c305` _(2026-09-30: BeatGrantTTL = MemberTTL, `5hek`; `/status`
  gitops row, `9l67`)_.
- **Cluster endpoint is a LAN address** (`https://10.0.0.68:6443`); the
  cluster needs no mesh to be a cluster (invariant 4 unqualified).
- **w1 is off** (owner closed it 2026-09-21 ~07:20Z; it will come
  back) and **carries `node.kubernetes.io/out-of-service=nodeshutdown:
  NoExecute`** since 2026-09-29 19:10Z — remove it before w1 rejoins.
  The taint released its ghost pods and volume attachments; the
  gateway and win2k25 run elsewhere. When w1 returns its kit is
  expired (`5hek`): re-serve its config (notes 2026-09-29).

## Cluster — _verified 2026-09-22_

- Three nodes, Talos v1.12.6 (kernel 6.18.18), k8s v1.32.3, containerd
  2.1.6. **One fleet image** declared in all three `talos/hardware/*.yaml`
  and running on every node:
  `ghcr.io/marnyg/talos-installer:v1.12.6-p0agent-0.1.6@sha256:d9193308…`
  (imager-built, ADR-0023: stock Talos + `iscsi-tools` v0.2.0 +
  `util-linux-tools` 2.41.2 + `p0agent` 0.1.6 — ships nodeagent
  `7fb6473`, expired speak-as → re-enroll, `9af0`). Extensions on cp1
  confirmed exactly those three; `ext-p0agent` Running (restarted by
  the 2026-09-21 apply, no reboot), `ext-iscsid` Running. No `nebula0`,
  no `ext-nebula`.
  - **cp1** — control plane, dir `talos/machines/b0-41-6f-15-3b-8f`,
    node name `talos-wu6-eib` (generated; **hostname not pinned**, `t7b2`
    pins it at the next reinstall), **static `10.0.0.68` on `eno1`**
    (`deviceSelector.hardwareAddr`, `dhcp: false`, gw/resolver
    `10.0.0.1`). NodeId `7dd90eb3…`, key on EPHEMERAL at
    `/var/lib/p0agent/key`. Mini-PC; STATE + EPHEMERAL (LUKS2, capped)
    + former `u-media` partition now Longhorn's (322GB).
  - **w1** — worker, dir `talos/machines/0c-37-96-5d-26-c4` (the r8152
    USB dongle's MAC since 2026-10-01, `c4vd`; was the Dell
    pass-through `98-e7-43-11-97-b8` it first provisioned through —
    the box has no wired PCI NIC), node name `w1` (**pinned**),
    **static `10.0.0.71`** on that dongle (selector by the same MAC). NodeId
    `40c9d1ca…`. Alienware x15 R1, i9-11900H, 1TB NVMe: STATE (LUKS2) +
    EPHEMERAL 200GiB (LUKS2) + `u-longhorn` 700GiB (xfs, unencrypted —
    ADR-0004 posture) at `/var/mnt/longhorn`. Beware `sda`, a USB boot
    stick — install disk is pinned. Worker configs take
    `clusters/homelab/worker-{cluster,secrets}.yaml`.
    **Off since 2026-09-21 07:23Z** (`Ready=Unknown`, no ping, no apid).
  - **nas1** — worker, the storage node, dir
    `talos/machines/6c-bf-b5-05-51-a8`, node name `nas1` (**pinned**),
    **static `10.0.0.74`** (selector by the directory MAC). NodeId
    `90cf67ec…`, enrolled at first boot (`beat ok`, facets
    `[apid kube-api]`). TerraMaster F4-425 Plus, 4x800MHz / 7.5GiB RAM,
    1.0TB Kingston SNV3S1000G NVMe: STATE 105MB + EPHEMERAL 86GB (both
    LUKS2) + `u-longhorn` **912GB** (xfs, unencrypted — ADR-0004
    posture, same open thread `8e46f3a5`) at `/var/mnt/longhorn`.
    Install disk is `diskSelector: type: nvme` — `sda` is a 2.1GB USB
    "Flash Disk", the same trap w1 has. **Two of four SATA bays filled**
    _(2026-10-04, `lug3`)_: `u-longhorn-1` (`sdd`, wwid
    `naa.5000039eb8db62c2`) and `u-longhorn-2` (`sde`,
    `naa.5000039eb8db61d8`), TOSHIBA MN10ADA4 4 TB each, xfs,
    unencrypted, at `/var/mnt/longhorn-{1,2}` — selected by **WWID**
    (these report no serial), applied live, no reboot. The remaining
    two bays follow the same recipe in `patch.yaml`. Longhorn's label
    is `create-default-disk=config` + a `default-disks-config`
    annotation (NVMe tag `nvme`, bays `bulk`) — what a reinstall
    reproduces; the live Node CR got `bulk-1`/`bulk-2` by hand because
    Longhorn reads the annotation only for a node without disks (cp1's
    and w1's `=true` labels are declared in their patches since
    2026-09-22).
    **Both 2.5GbE ports matter**: `:a8` and `:a9` are consecutive, the
    device flow reports `${mac}` from the first (`:a8`) regardless of
    where the cable is, so the cable must stay in `:a8` — see
    `notes.md` 2026-09-22.
    SMBIOS is the OEM placeholder (serial `Default string`, UUID
    `03000200-0400-…-000700080009`), declared in `meta.yaml` with its
    KMS-allowlist caveat; seal/unseal work and no `UNDECLARED` warning
    was raised.
- **Cluster endpoint `https://10.0.0.68:6443`** _(P2.5, `359.9.5`,
  2026-09-20)_: kube-apiserver SANs `cp1, cp1.mesh.internal,
  talos-wu6-eib, 10.0.0.68, 10.96.0.1` — no overlay address. etcd
  advertises `10.0.0.68:2380/2379` (declared, so `6gq`'s lease-drift
  failure cannot recur). The router's DHCP pool is deliberately not
  edited (decision `ebis`); a pool collision is accepted risk.
  Changing the endpoint rotated the SA issuer — 14 control-loop pods
  had to be recreated by hand (`etzl` for the runbook). Predecessors:
  DHCP lease → wg0 → nebula `10.42.218.125` → LAN static.
- **Admin access** is over the identity plane from the Mac's
  `irohup -tun` daemon (`talos-mesh`, `/var/lib/talos-mesh/marius-mac.iroh`,
  utun `198.18.0.1`, ADR-0025): `talos/talosconfig` + `kubeconfig`
  (local, gitignored) use `-e cp1.mesh.internal`; **`-n` must be the
  LAN IP** (`-n 10.0.0.68`) — apid resolves `-n` names with the node's
  own resolver, which has no mesh zone (gotchas.md; goes away with
  `t7b2`). `nix run .#apply` reaches both nodes and the hub this way.
  Recovery path: LAN address SANs + owner keys, no hub needed.
- **Service exposure** (ADR-0026): the `gateway` Deployment (ns
  `gateway`, a `nodeagent` member of kind gateway, key + Kit on the
  64Mi RWO PVC `gateway-state`) terminates `ingress-http` and
  reverse-proxies to a ClusterIP-only ingress-nginx (no hostNetwork,
  PSS baseline), injecting `X-Mesh-Node/Name/Groups`; `jellyfin` is a
  raw TCP splice to :8096. **ingress-nginx runs 2 replicas** on
  distinct nodes (required anti-affinity, PDB minAvailable 1,
  `maxUnavailable: 1`) _(2026-09-30, `9l67` slice 2)_. Ingress hosts, all `<svc>.gw.mesh.internal`:
  argocd, auth, oauth2, jackett, jellyfin, nzbget, radarr, sonarr,
  transmission. No `*.cp1` names remain. No web NodePorts remain
  (Jellyfin's 30096 and win2k25's RDP 30389 cut 2026-10-04 — a v3 node
  forwards only its own facets, so they were dead doors); only
  transmission's peer ports are NodePorts.
- **Every web UI authenticates against the wallet** _(since
  2026-07-31)_: SIWE→OIDC bridge `auth.gw.mesh.internal` (ns `sso`) is
  the only IdP — ArgoCD native OIDC (local `admin` = break-glass), the
  five media UIs behind oauth2-proxy `auth_request`, Jellyfin via
  jellyfin-plugin-sso (local login kept for the TV). A bridge restart
  rotates the JWKS — re-sign, nothing lost. Known gap: the bridge
  hardcodes `groups: ["admins"]` (`5kh`). **oauth2-proxy runs 2
  replicas** on distinct nodes (anti-affinity, PDB, `maxUnavailable:
  1`; cookie-backed sessions make it replica-safe); **siwe-oidc is 1
  replica by design** (per-pod signing key and auth codes, `4ze8`)
  with 30 s unreachable/not-ready tolerations so it fails over in
  about a minute _(2026-09-30, `9l67` slice 2)_.
- Media stack pods split across both nodes on Longhorn RWX volumes;
  SealedSecrets (`newshosting`, `nzbgeek`) unseal via the
  inlineManifest-provisioned key pair. **Reinstall**:
  [`guides/reinstall.md`](guides/reinstall.md) — label-scoped reset
  only; a plain `talosctl reset` wipes the Longhorn disk.
- **Sovereign-actor provisioner** _(2026-09-29, `0bc.4.6`)_: ns `sap`
  (PSS `restricted`), Deployment `sap-provisioner` (`-driver k8s`,
  image `ghcr.io/marnyg/sap-actors:53b84b4@sha256:f9de434d…`, public
  on GHCR), SA + namespaced Role on `batch/jobs` only, key on the 16Mi
  RWO PVC `sap-provisioner-state`. Actor id `ed:9c3ae5ec…`; one
  `-customer`, the owner's laptop parent `ed:79547a96…` (key
  `~/.sap-parent/key`). Children run as Jobs in `sap`; none live
  between runs. Egress only (fly relay); no Service. Image since
  2026-09-29 (`0bc.6`): `47bae97@sha256:879d6580…`, and the flag
  `-lighthouse=ed:5cad808a…`.
- **Sovereign-actor lighthouse** _(2026-09-29, `0bc.6`)_: Deployment
  `sap-lighthouse` in `sap` (same image, `command: lighthouse`, 16Mi
  RWO PVC `sap-lighthouse-state`, no SA/RBAC/Service, egress only).
  Actor id `ed:5cad808a3ed6be76…` (full id in the provisioner
  manifest); `-member`: the provisioner `ed:9c3ae5ec…` (publishes
  each beat) and the laptop parent `ed:79547a96…` (looks up). The
  parent finds the provisioner with `spawn -lighthouse <L> 
  -provisioner-id <P>` — nothing is read off a pod. Directory is
  volatile: a restart empties it until the provisioner's next beat
  (≤ 1 min).
- **ArgoCD** _(verified 2026-09-30)_: `apps` Synced/Healthy at
  `64dbc94`, ops completing. `argocd-application-controller` is pinned
  to the control plane (`k8s/apps/argocd/controller-patch.yaml`).
  The hub's `/status` **gitops** row reads the root app over cp1's
  `kube-api` every 5 min and warns on a reconcile > 20 min old or a
  sync op Running > 30 min (`config-server/clusterwatch.go`). The 09-29
  hang (op waiting on the ghost gateway pod) ended with the w1 taint.
- Provenance of the image line: 2026-09-15 cp1 first booted an
  imager build (`p0agent` 0.0.3, scratch relay); 2026-09-16 it became
  the declared image (ADR-0023); 2026-09-19/20 w1 followed and both
  moved to 0.1.5 without nebula (P4.1, `359.11.1`); 2026-10-04 all
  three upgraded to 0.1.6 (`9af0`), the reboot doubling as the slot-1
  re-key (`spvd`).

## Mesh (identity plane) — _verified 2026-09-21_

- **Members and kits.** Each member holds a Kit: its member cert
  (`aud` = its NodeId, `cav.name`, `cav.groups`, 90 d) + the beat
  grant for `#renew`/`#bundle` (90 d since `c33c305`; older kits
  re-issue at 90 d on their first renewal) + `invoke` grants compiled
  from `talos/mesh-policy-v3.yaml` (7 d, renewed on the daily beat) +
  its own self-issued `reach-me-at` (1 h). Gateway re-enrolled
  2026-09-30 (same NodeId `ed:45fc82fc…`, image `ff7c478`). Groups in
  use: `admins`, `machines`, `media`. Enrolled: cp1, w1 (machines,
  auto at boot via single-use token), gateway (headless device flow),
  `marius-mac` (admins), `phone` (media; Sony XQ-BQ52, 2026-09-20).
  Name→NodeId is witnessed from the beat, not compiled (ADR-0024).
- **Paths.** Remote members relay through the hub (ADR-0006/0022);
  same-LAN members hole-punch direct — phone on home Wi-Fi measured
  LAN-direct to the gateway (`*ip:10.0.0.67`), on 5G `*relay`, HTTP
  200 in 0.10 s (2026-09-20). Throughput/4K playback over the relay is
  **not yet measured** on the new plane (P0.2 spike figures only;
  ADR-0013's TV gate `4te`).
- **Presentation.** `config-server/fakeip` + `meshtun` on every device
  — utun on macOS (`irohup -tun`), `VpnService` fd on Android — answer
  `*.mesh.internal` from the witnessed name map with per-device fake
  IPs in `198.18/15`, split-route only that range, forward all other
  DNS to the underlay. Zone rule: `<svc>.<member>` resolves only when
  `<member>` is a gateway (kind read from `reach-me-at`'s facets).
  Known wart: presentations advertise their own tun address as a
  direct endpoint (`bh74`).
- **Policy.** One recipe, `talos/mesh-policy-v3.yaml` (ADR-0017;
  Nickel contract `verification/nickel/mesh-policy-v3.ncl`): node
  `apid`/`kube-api` for admins + the hub's own `apid` and `kube-api`
  rows; gateway
  `ingress-http` for admins and media, `jellyfin` for media; hub
  `hub-http`. No receiver holds a table; the blocklist is the git list.
- **Stale binaries** (`5q33`, P3): phone/TV APK and the Mac daemon
  predate `600d2d4`; harmless while every member holds a kit, fails
  only at a *new* enrollment (the gateway's did, 2026-09-30, until
  rebuilt).

## Hub on fly — _verified 2026-10-04_

- App `marnyg-talos-config`, region `arn`, one `shared-cpu-1x`/256 MB
  machine (`7817426a194968`), **image
  `registry.fly.io/marnyg-talos-config:925dc9d`** (nix-built
  `fly/image.nix`: static `config-server -tags iroh` + static
  `iroh-relay` + busybox; `talos/` comes from the signed git tip,
  ADR-0030), deployed 2026-10-04 15:43Z via `fly/deploy.sh` from the
  nixos box and unsealed by the owner with both signatures (`speak-as`
  + `MasterMessage`). First nebula-free image was `0e67661`
  (2026-09-20).
- **hubkey `ed:60669a3c…`** (ephemeral, new on every unseal; was
  `a65c301d…`) speaks for `0xf568…9406` (`speak-as` cert,
  groups `admins machines media`, verbs `member invoke`), served at
  `/.well-known/talos-hub/{speak-as,reach-me-at}`. `/sealed` → 200.
  Auto-bootstrap read `etcd-running` off cp1 over the plane 8 s after
  unseal, the hub calling as an ordinary member (`HubMemberName`).
- **Ports** (invariant 5): `http_service` 443→8080 (web + `hub-http`
  facet + the iroh relay child on loopback `:3340`, proxied by fly's
  TLS, ADR-0022 — `IROH_RELAY_URL=https://marnyg-talos-config.fly.dev`);
  `tcp/8443` → 8081 for KMS disk unseal (`KMS_ADVERTISE`). **No UDP.**
  The dedicated IPv4 `213.188.219.215` stays only because fly's shared
  v4 carries 80/443 alone and KMS is on 8443 (`os8s` to fold it).
  `auto_stop_machines = "stop"`, `min_machines_running = 1`.
- `fly secrets list` is **empty**. Everything derives from the two
  unseal signatures: the ephemeral hubkey's authority (`speak-as`),
  KMS seal keys, recovery passphrases, and the age identity that
  decrypts `clusters/**/*.age` into tmpfs (`masterderive`). A wrong
  wallet fails at the age decrypt (no CA pin any more). Public age
  recipient committed at `talos/age-recipient.txt`; the SSH key remains
  a break-glass recipient. An unseal that cannot decrypt fails loudly.
- The relay runs while the hub is sealed (it holds no key); relay
  access gating beyond the blocklist is `5gz`.

## Disk encryption posture — _decision, closed 2026-07-24_

Slot 0 is the network KMS, slot 1 a derived static passphrase stored in
plaintext META.

- **Slot 0 is live at boot** _(2026-10-04, `spvd`; was "dormant")_:
  the first volume's slot-0 call loses a 2–5 s race to DHCP (`network
  is unreachable`) and slot 1 opens it; the KMS answers for everything
  after, and Talos re-syncs whichever slot it could not verify. A
  volume whose slots all fail is retried every 30 s. Observed on all
  three nodes; proven by re-keying (below).
- **Accepted consequence**: encryption protects against disk
  disposal/RMA only, not against an attacker with the running machine.
- A sealed hub therefore does **not** block reboots — slot 1 boots the
  node unattended. Only provisioning, config refetch and a slot-1
  re-key need an unseal.
- Going KMS-only would first require break-glass tooling for slot-0
  blobs.
- **Slot 1 derives from the UUID on every installed machine**
  _(2026-10-03 rule, 2026-10-04 fleet)_: `RecoveryPassphrase(master,
  uuid)`; `recover -recovery -uuid <uuid>`. The rule: a secret derives
  only from a handle that outlives it; the MAC is the config *selector*
  and a NIC swap renames it (w1, `c4vd`), which rotated the composed
  passphrase unnoticed. The three pre-rule installs were grandfathered
  under `installMAC:` for one day and re-keyed 2026-10-04: two boots
  per node with the hub unsealed (boot A lands the config in META and
  re-keys EPHEMERAL; boot B re-keys STATE via slot 0). `nix run
  .#apply` dry-runs first and refuses an encryption diff without
  `APPLY_REKEY=1`, and a reboot without `APPLY_REBOOT=1`. nas1's UUID
  is the OEM placeholder: a second such box under the same master
  would share its passphrase — owner-local, accepted as the KMS
  allowlist caveat already is.

> Recorded in **ADR-0004**, including the consequence that matters
> most: wipe META before a *machine* (not just a disk) leaves the
> owner's hands, because the slot-1 passphrase travels with it.

## Storage — _verified 2026-07-31; state 2026-10-04_

Longhorn 1.12.0 via ArgoCD (`k8s/apps/longhorn/application.yaml`).
ADR-0011.

- **Disks are opt-in per node** (`createDefaultDiskLabeledNodes: true`),
  only nodes labeled `node.longhorn.io/create-default-disk=true` get
  one — without it Longhorn would put replicas on EPHEMERAL scratch.
  - `w1` — `default-disk-1030500000000` at `/var/mnt/longhorn`, 751GB,
    tag `nvme` _(2026-10-03)_.
  - `talos-wu6-eib` (cp1) — `default-disk-1030400000000`, 322GB (the
    former `u-media` partition, handed over 2026-07-31), tag `nvme`
    _(2026-10-03)_.
  - `nas1` — `default-disk-1030500000000` at `/var/mnt/longhorn`, 911GB,
    tag `nvme`; **`bulk-1`/`bulk-2`** at `/var/mnt/longhorn-{1,2}`,
    3998GB each, tag `bulk` _(2026-10-04)_.
  - Raw: ~1984GB NVMe tier + 7996GB bulk tier; `storageReserved: 0`.
- StorageClasses: `longhorn` (default; RWO, 2 replicas, `Delete`) for
  app state — **users: `gateway-state` (64Mi)**, `sap-*-state`,
  win2k25's volumes; every media app still keeps config on `emptyDir`.
  **Fenced to `diskSelector: nvme`** since 2026-10-03 (`jx78`, chart
  `persistence.defaultDiskSelector`); all cp1/w1/nas1 `/var/mnt/longhorn`
  disks carry the tag, the existing Volume CRs were patched and three
  replicas that had landed on the bays deleted (Longhorn rebuilt them
  on `nvme`; per-replica `evictionRequested` gets cancelled by the node
  controller unless the disk itself is being evicted). `longhorn-bulk` (RWX,
  `Retain`) for `media/{tv,movies,downloads}` is since 2026-10-04 a
  **mirror inside nas1**: 2 replicas, `diskSelector: bulk`, soft node /
  hard disk anti-affinity — one replica per bay, none on w1 (cnb5's
  placement half; the class was deleted+recreated, the Volume CRs
  patched, the w1 replicas evicted). Its **share-managers (NFS
  servers) are pinned to nas1** (`shareManagerNodeSelector`) — the
  scheduler had put all three on w1, so data crossed w1's USB dongle
  twice. Sizes 900/400/200Gi. **Library imported 2026-10-04** from the
  old docker host (10.0.0.11, `~/disks/1TB-old/server`): 368GB tv
  (37 shows) + 98GB movies by rsync through a one-off NodePort pod
  (`scripts/media-import.yaml`, deleted after), ~105MB/s wired; Sonarr's
  29 monitored series re-added by API (246/246 episode files found).
  The old docker media containers are **stopped**, not removed.
- **With w1 off** _(2026-09-30)_: Longhorn node `w1` not ready; the
  three media volumes `faulted` (single replica, on w1 — back when w1
  is); `gateway-state` attached healthy (gateway on nas1), win2k25's
  two volumes attached degraded on cp1. `nodeDownPodDeletionPolicy:
  delete-both-statefulset-and-deployment-pod` since `c33c305`, so the
  next node loss releases RWO volumes without a hand-applied taint.
- Volume mobility was verified 2026-07-31 (write on cp1, read after
  reschedule to w1). `talosctl wipe disk` needs `--drop-partition` to
  actually free a user volume. The chart's `preUpgradeChecker` job is
  disabled (ArgoCD PreSync ordering); chart version pinned exactly.
- **No backup target configured.** Replication is not backup, and
  invariant 2 makes Longhorn's own bookkeeping a backup problem — so a
  cp1 wipe is not yet the routine act `reinstall.md` describes.
