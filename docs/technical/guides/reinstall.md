# Reinstall runbook

> 🚧 **Provisional — this guide is scoped to cp1 and to ADR-0008, which
> [ADR-0011](../adrs/0011-longhorn-replaces-hostpath-and-the-preserved-media-volume.md)
> (Proposed) supersedes.** Two things are already out of date:
>
> - **The warning below protects a volume that is becoming ordinary.**
>   Once media lives on Longhorn, `u-media` holds nothing unique and a
>   whole-disk reset costs a replica rebuild, not a library. Under
>   ADR-0011 the label-scoped reset stops being the point of this file.
> - **It assumes one node.** w1's reinstall (2026-07-31) needed the
>   *opposite* of the advice here — a deliberate bare `talosctl reset`,
>   because repartitioning was the goal and the node held nothing. See
>   "Wiping a worker" at the bottom.
>
> Rewrite once ADR-0011 is Accepted. Until then the cp1 procedure below
> is still correct **for cp1**.

How to take cp1 from "wipe it" back to a working member **without
losing the media library**. Current addresses, disk layout and cluster
facts live in [`../deployed-state.md`](../deployed-state.md); this file
is only the procedure.

> ⚠️ **Never run a bare `talosctl reset` on cp1.** It wipes the whole disk
> including the `u-media` partition, so the media library dies and
> recovery needs USB/PXE. The label flags below are what make the
> reinstall routine (ADR-0008).

## Preconditions

- Physical or LAN access to the node — the mesh dies with the reset, so
  the node comes back reachable only on a DHCP lease.
- The hub is **unsealed**: `curl -so /dev/null -w '%{http_code}\n'
  https://marnyg-talos-config.fly.dev/sealed` must return `200`
  (`503` = sealed, or the mesh failed to start). A sealed hub cannot
  serve the composed config, and the node will sit in maintenance mode.
- Owner wallet available in case the hub needs an unseal.

## Steps

1. **Reset, label-scoped.** Deletes STATE + EPHEMERAL, keeps the
   bootloader and `u-media`:

   ```bash
   # -n must be an IP: apid resolves the node name itself and the node's
   # DNS has no .mesh.internal zone (see ../deployed-state.md).
   talosctl --talosconfig talos/talosconfig -n <node-ip> -e <node-ip> \
     reset --graceful=false --reboot \
     --system-labels-to-wipe STATE --system-labels-to-wipe EPHEMERAL
   ```

2. **Find the node again.** It reboots into maintenance mode on a LAN
   DHCP lease (the lease drifts — do not assume the old one).

3. **Apply the hub-composed config.** Never compose locally:

   ```bash
   nix run .#apply -- <mac>          # over the mesh, when it exists
   # maintenance-mode path, from the LAN:
   talosctl apply-config --insecure -n 10.0.0.<lease> --file <composed>
   ```

4. **Bootstrap etcd.**

   ```bash
   talosctl --talosconfig talos/talosconfig -n <node-ip> -e <node-ip> bootstrap
   ```

5. **Verify the library survived.** The partition should be re-adopted
   by its `u-media` label, already mounted:

   ```bash
   talosctl -n <node-ip> -e <node-ip> get volumestatus | grep u-media
   talosctl -n <node-ip> -e <node-ip> get mountstatus  | grep u-media
   talosctl -n <node-ip> -e <node-ip> usage -d 2 /var/mnt/media
   ```

   Cluster state itself rebuilds from git via ArgoCD.

## What a reinstall legitimately forgets

- **Kept**: the media *files* under `/var/mnt/media` (`u-media`).
- **Lost**: everything on EPHEMERAL — etcd, images, logs, and the media
  apps' own databases (sonarr/radarr/jellyfin metadata, watch state).
  Files survive, library metadata does not. That is the designed
  outcome, not a failure.
- **Also re-verify afterwards**: anything applied live by hand rather
  than through git. The installer job encodes the ArgoCD OIDC config
  and oauth2-proxy wiring at bootstrap time only, so check SSO once the
  stack is back.

## Wiping a worker (added 2026-07-31)

Different problem, opposite advice. A worker holds no etcd and, until
Longhorn lands, no unique data — so the constraint that shapes the cp1
procedure above simply is not there.

**Repartitioning requires the bare reset.** Partition geometry is fixed
at creation: capping EPHEMERAL or adding a user volume in `patch.yaml`
does nothing to an installed node, and the label-scoped reset
deliberately *preserves* the layout. Only a whole-disk wipe re-reads it.

```bash
# 1. commit the geometry, then push it to the hub
fly deploy                       # re-seals: unseal at /status afterwards

# 2. wipe (this is the bare reset the cp1 section warns against —
#    correct here, because repartitioning is the goal)
talosctl -e <name>.mesh.internal -n <mesh-ip> reset --graceful=false --reboot

# 3. the node PXE boots and restarts its device flow — approve on /status

# 4. verify the new geometry
talosctl -e <name>.mesh.internal -n <mesh-ip> get volumestatus
talosctl -e <name>.mesh.internal -n <mesh-ip> get mountstatus
```

Notes from doing this to w1:

- **Do it while the node is empty.** The cost only grows.
- The mesh address and the KMS allowlist survive untouched: both derive
  from (master, MAC) and (master, UUID), so the node comes back with the
  same identity and unlocks its own disk unattended.
- **The DHCP lease will move** (w1: `.36` → `.38`). Nothing should care;
  if something does, that is the bug.
- **Pin the hostname** in `patch.yaml`, or the node returns under a new
  generated name and the old `Node` object lingers as `NotReady` —
  delete it with `kubectl delete node <old-name>`.
