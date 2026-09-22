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
  `4518c2f`.
- **Cluster endpoint is a LAN address** (`https://10.0.0.68:6443`); the
  cluster needs no mesh to be a cluster (invariant 4 unqualified).
- **w1 is off** (owner closed it 2026-09-21 ~07:20Z). With it, the
  gateway pod and every `*.gw.mesh.internal` service are down until
  Longhorn releases the gateway's RWO volume to cp1 — filed as the HA
  sweep `talos-config-9l67`; not being fixed by hand.

## Cluster — _verified 2026-09-22_

- Three nodes, Talos v1.12.6 (kernel 6.18.18), k8s v1.32.3, containerd
  2.1.6. **One fleet image** declared in all three `talos/hardware/*.yaml`
  and running on every node:
  `ghcr.io/marnyg/talos-installer:v1.12.6-p0agent-0.1.5@sha256:3c2c7cc3…`
  (imager-built, ADR-0023: stock Talos + `iscsi-tools` v0.2.0 +
  `util-linux-tools` 2.41.2 + `p0agent` 0.1.5). Extensions on cp1
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
  - **w1** — worker, dir `talos/machines/98-e7-43-11-97-b8` (Dell
    pass-through MAC: only a dock carries it; the box has no wired PCI
    NIC — a reinstall needs the dock or a renamed dir, `c4vd`), node
    name `w1` (**pinned**), **static `10.0.0.71`** on the r8152 USB
    dongle `0c:37:96:5d:26:c4` (selector by *that* MAC). NodeId
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
    "Flash Disk", the same trap w1 has. **Four SATA bays are empty**;
    each becomes its own `UserVolumeConfig` by disk serial, applied
    live (no reinstall). Longhorn's
    `node.longhorn.io/create-default-disk=true` is declared in
    `nodeLabels` — cp1's and w1's were applied by hand and still are.
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
  raw TCP splice to :8096. Ingress hosts, all `<svc>.gw.mesh.internal`:
  argocd, auth, oauth2, jackett, jellyfin, nzbget, radarr, sonarr,
  transmission. No `*.cp1` names remain. The only web NodePort left is
  Jellyfin's 30096 for LAN-direct clients, plus transmission's peer
  ports.
- **Every web UI authenticates against the wallet** _(since
  2026-07-31)_: SIWE→OIDC bridge `auth.gw.mesh.internal` (ns `sso`) is
  the only IdP — ArgoCD native OIDC (local `admin` = break-glass), the
  five media UIs behind oauth2-proxy `auth_request`, Jellyfin via
  jellyfin-plugin-sso (local login kept for the TV). A bridge restart
  rotates the JWKS — re-sign, nothing lost. Known gap: the bridge
  hardcodes `groups: ["admins"]` (`5kh`).
- Media stack pods split across both nodes on Longhorn RWX volumes;
  SealedSecrets (`newshosting`, `nzbgeek`) unseal via the
  inlineManifest-provisioned key pair. **Reinstall**:
  [`guides/reinstall.md`](guides/reinstall.md) — label-scoped reset
  only; a plain `talosctl reset` wipes the Longhorn disk.
- Provenance of the image line: 2026-09-15 cp1 first booted an
  imager build (`p0agent` 0.0.3, scratch relay); 2026-09-16 it became
  the declared image (ADR-0023); 2026-09-19/20 w1 followed and both
  moved to 0.1.5 without nebula (P4.1, `359.11.1`).

## Mesh (identity plane) — _verified 2026-09-21_

- **Members and kits.** Each member holds a Kit: its member cert
  (`aud` = its NodeId, `cav.name`, `cav.groups`, 90 d) + `invoke`
  grants compiled from `talos/mesh-policy-v3.yaml` (7 d, renewed on
  the daily beat) + its own self-issued `reach-me-at` (1 h). Groups in
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
  `apid`/`kube-api` for admins + the hub's own `apid` row; gateway
  `ingress-http` for admins and media, `jellyfin` for media; hub
  `hub-http`. No receiver holds a table; the blocklist is the git list.
- **Stale binaries** (`5q33`, P3): phone/TV APK, gateway image and the
  Mac daemon still send `enrollmsg` v2; harmless while every member
  holds a kit, fails only at a *new* enrollment.

## Hub on fly — _verified 2026-09-21_

- App `marnyg-talos-config`, region `arn`, one `shared-cpu-1x`/256 MB
  machine (`7817426a194968`), **image `registry.fly.io/marnyg-talos-config:0e67661`**
  (nix-built `fly/image.nix`: static `config-server -tags iroh` +
  static `iroh-relay` + tracked `talos/` + busybox), deployed
  2026-09-20 22:20Z via `fly/deploy.sh` — the first nebula-free image.
  Unsealed 22:20:27Z with both signatures (`speak-as` + `MasterMessage`).
- **hubkey `a65c301d…`** speaks for `0xf568…9406` (`speak-as` cert,
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

- **Slot 0 is dormant at boot in practice**: early-boot DNS loses the
  race to the KMS dial every time so far. Accepted rather than fixed.
- **Accepted consequence**: encryption protects against disk
  disposal/RMA only, not against an attacker with the running machine.
- A sealed hub therefore does **not** block reboots — slot 1 boots the
  node unattended. Only provisioning and config refetch need an unseal.
- Going KMS-only would first require break-glass tooling for slot-0
  blobs.

> Recorded in **ADR-0004**, including the consequence that matters
> most: wipe META before a *machine* (not just a disk) leaves the
> owner's hands, because the slot-1 passphrase travels with it.

## Storage — _verified 2026-07-31; state 2026-09-21_

Longhorn 1.12.0 via ArgoCD (`k8s/apps/longhorn/application.yaml`).
ADR-0011.

- **Disks are opt-in per node** (`createDefaultDiskLabeledNodes: true`),
  only nodes labeled `node.longhorn.io/create-default-disk=true` get
  one — without it Longhorn would put replicas on EPHEMERAL scratch.
  - `w1` — `default-disk-1030500000000` at `/var/mnt/longhorn`, 751GB.
  - `talos-wu6-eib` (cp1) — `default-disk-1030400000000`, 322GB (the
    former `u-media` partition, handed over 2026-07-31).
  - Total raw 1073GB, `storageReserved: 0` on both.
- StorageClasses: `longhorn` (default; RWO, 2 replicas, `Delete`) for
  app state — **users: `gateway-state` (64Mi)** and nothing else, every
  media app still keeps config on `emptyDir`; `longhorn-bulk` (RWX, 1
  replica, `Retain`) for `media/{tv,movies,downloads}` (200/200/50Gi).
- **2026-09-21 with w1 off**: Longhorn node `w1` not ready; the three
  media volumes `faulted` (single replica, on w1), `gateway-state` and
  two others `attaching` — the gateway's replacement pod on cp1 sits in
  `Multi-Attach error` until the old attachment is released. Expected
  to self-heal when w1 returns; the structural fix is `9l67`.
- Volume mobility was verified 2026-07-31 (write on cp1, read after
  reschedule to w1). `talosctl wipe disk` needs `--drop-partition` to
  actually free a user volume. The chart's `preUpgradeChecker` job is
  disabled (ArgoCD PreSync ordering); chart version pinned exactly.
- **No backup target configured.** Replication is not backup, and
  invariant 2 makes Longhorn's own bookkeeping a backup problem — so a
  cp1 wipe is not yet the routine act `reinstall.md` describes.
