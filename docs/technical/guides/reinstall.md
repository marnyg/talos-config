# Reinstall runbook

How to take any node from "wipe it" back to a working member. Current
addresses, disk layout and cluster facts live in
[`../deployed-state.md`](../deployed-state.md); this file is only the
procedure.

**The thesis, per ADR-0011 and invariant 2: no single node's disk holds
anything unique.** A wipe is the whole disk, every time. What survives a
reinstall survives because it is re-derivable from git or replicated
elsewhere — never because a partition was spared. If you find yourself
wanting to preserve a label to protect data, that data is in the wrong
place; fix the placement, not the reset command.

The node's *identity* is not on the disk either: the mesh address and
the KMS allowlist entry derive from (master, MAC) and (master, UUID), so
a wiped node comes back on the same mesh address and unseals its own
disk unattended.

## Preconditions

- Physical or LAN access to the node — the mesh dies with the reset, so
  the node comes back reachable only on a DHCP lease.
- The hub is **unsealed**: `curl -so /dev/null -w '%{http_code}\n'
  https://marnyg-talos-config.fly.dev/sealed` must return `200`
  (`503` = sealed, or the mesh failed to start). A sealed hub cannot
  serve the composed config, and the node will sit in maintenance mode.
- Owner wallet available: a hub deploy (`fly/deploy.sh`) re-seals, and the returning node
  needs an approval signature.
- **Geometry is committed first.** Partition layout is fixed at
  creation: capping EPHEMERAL or adding a user volume in `patch.yaml`
  does nothing to an installed node. Commit and deploy the hub *before*
  the wipe, or the node comes back with the old layout.
- **The directory MAC is the MAC the node will boot with.** The node
  fetches `/config?mac=<NIC it booted on>`; a directory named after
  any other address serves nothing and the node sits in maintenance
  mode. w1's directory was the laptop's Dell pass-through MAC (a Dell
  dock inherits it) until 2026-10-01, when it was renamed to the r8152
  dongle's — if the dongle is swapped, rename the directory again
  (`git mv`) and deploy the hub. Under v3 the role owns a name and KMS
  is keyed by UUID, so the rename moves nothing (2026-10-03, hub test
  `TestDiskEncryptionSurvivesRename`; the pre-rule installs carried
  `installMAC:` until their 2026-10-04 re-key). `apply` refuses a
  diff that touches `systemDiskEncryption` unless `APPLY_REKEY=1`
  — a deliberate re-key is two boots with the hub unsealed (ADR-0028
  consequences).
- **cp1 only — pin the hostname in the same commit** (`t7b2`). cp1
  runs under Talos' generated `talos-wu6-eib` because a live
  `machine.network.hostname` change registers a *new* Node (and etcd
  member name, and Longhorn node) while the old ones linger: every
  volume with a replica on cp1 degrades until the old Longhorn node CR
  is deleted and the disk re-adopted — on the only control plane. A
  wipe does all of that anyway, so the reinstall is the one moment the
  rename is free: before the
  wipe, add `hostname: cp1` under `machine.network` in
  `machines/b0-41-6f-15-3b-8f/patch.yaml` (w1's patch is the model),
  delete the `hostname: talos-wu6-eib` override from its `meta.yaml`,
  change `nodes:` in `talos/talosconfig` to `cp1`, drop
  `talos-wu6-eib` from `deployed-state.md`, deploy the hub, then
  wipe. After: `kubectl delete node talos-wu6-eib` and delete the
  Longhorn `nodes.longhorn.io/talos-wu6-eib` CR once its replicas are
  gone.

## Steps

1. **Reset, whole disk.**

   ```bash
   # -n must be an IP: apid resolves the node name itself and the node's
   # DNS has no .mesh.internal zone (see ../deployed-state.md).
   talosctl --talosconfig talos/talosconfig -n <node-ip> -e <node-ip> \
     reset --graceful=false --reboot
   ```

2. **Find the node again.** It reboots into maintenance mode on a LAN
   DHCP lease. **The lease will move** (w1: `.36` → `.38`) — do not
   assume the old one. Nothing should care about the lease; if something
   does, that is the bug.

3. **Approve the device flow.** The node restarts provisioning and waits
   for a wallet signature at `/status`. Until it is approved there is no
   apid, so the node is un-inspectable — this is normal.

4. **Apply the hub-composed config.** Never compose locally:

   ```bash
   nix run .#apply -- <mac>          # over the mesh, when it exists
   # maintenance-mode path, from the LAN:
   talosctl apply-config --insecure -n 10.0.0.<lease> --file <composed>
   ```

5. **Bootstrap etcd — control plane only.**

   ```bash
   talosctl --talosconfig talos/talosconfig -n <node-ip> -e <node-ip> bootstrap
   ```

6. **Verify the geometry, not the data.**

   ```bash
   talosctl -n <node-ip> -e <node-ip> get volumestatus
   talosctl -n <node-ip> -e <node-ip> get mountstatus
   kubectl get nodes                 # returns under its pinned hostname?
   ```

   Cluster state rebuilds from git via ArgoCD; volume replicas rebuild
   from their peers.

## What a reinstall legitimately forgets

- **Lost: everything on the disk.** EPHEMERAL, the user volume, etcd on
  a control plane, images, logs. This is the designed outcome.
- **Recovered from git:** all cluster state ArgoCD manages.
- **Recovered from peers:** Longhorn volumes, as a replica rebuild —
  *provided a replica exists on another node.* Replica count is the
  durability guarantee; a one-replica volume on the node you just wiped
  is simply gone.
- **Re-verify afterwards:** anything applied live by hand rather than
  through git. The installer job encodes the ArgoCD OIDC config and
  oauth2-proxy wiring at bootstrap time only, so check SSO once the
  stack is back.
- **Pin the hostname** in `patch.yaml`, or the node returns under a new
  generated name and the old `Node` object lingers as `NotReady` —
  delete it with `kubectl delete node <old-name>`.

## Where this is not yet cheap (2026-10-01)

The thesis above is the target state. Longhorn has been the data plane
since 2026-09-22 (ADR-0011; `longhorn` class 2 replicas, `longhorn-bulk`
1 replica) and the ingress path spreads across nodes (`9l67`), so a
**worker** wipe is cheap and routine: no etcd, no unique data — a
2-replica volume rebuilds from its peer. Two gaps keep a
**control-plane** wipe from being the same act:

- **etcd is single-node and there is no backup target** (`bsj`).
  Longhorn's volume/replica/snapshot CRDs live in etcd, so wiping cp1
  destroys Longhorn's bookkeeping and leaves every replica on nas1/w1
  as orphaned data needing salvage. Invariant 2 is explicit that this
  bookkeeping is a backup problem, not a git problem — so a backup
  target (and an etcd snapshot off the node) is the prerequisite for a
  routine cp1 wipe, not a nice-to-have.
- **`longhorn-bulk` volumes are single-replica** (`media/{tv,movies,
  downloads}`), so whichever node holds the replica takes the media
  library down with it — re-downloadable by ADR-0004's reasoning, but
  not free, and the symptom is silent (Jellyfin keeps Running on a
  hung NFS mount; w1 off 2026-09-21 → faulted volumes, noticed
  2026-10-01, `cnb5`). Check `kubectl get volumes.longhorn.io -n
  longhorn-system` for where they live before wiping *any* node; move
  them to nas1 once its bays are filled.

Do a worker wipe while the node is empty — the cost only grows.
