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
- Owner wallet available: `fly deploy` re-seals, and the returning node
  needs an approval signature.
- **Geometry is committed first.** Partition layout is fixed at
  creation: capping EPHEMERAL or adding a user volume in `patch.yaml`
  does nothing to an installed node. Commit and `fly deploy` *before*
  the wipe, or the node comes back with the old layout.

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

## Where this is not yet cheap (transitional, 2026-07-31)

The thesis above is the target state. Two gaps make a **control-plane**
wipe more expensive than a worker wipe today:

- **Longhorn is not deployed yet** (`ca77f427`, `214661d2`). Until it
  is, media PVs are `hostPath` on cp1 with no `nodeAffinity`
  (`0b374653`) and app state lives on EPHEMERAL. A cp1 wipe still loses
  sonarr/radarr/jellyfin metadata — the thing ADR-0008 never protected
  either.
- **etcd is single-node and there is no backup target.** Longhorn's
  volume/replica/snapshot CRDs live in etcd, so once Longhorn *is*
  deployed, wiping cp1 destroys Longhorn's bookkeeping and leaves
  replicas on other nodes as orphaned data needing salvage. Invariant 2
  is explicit that this bookkeeping is a backup problem, not a git
  problem — so a backup target is a prerequisite for a cp1 wipe being
  routine, not a nice-to-have.

Wiping a **worker** is already cheap and routine: no etcd, no unique
data. Do it while a node is empty — the cost only grows.
