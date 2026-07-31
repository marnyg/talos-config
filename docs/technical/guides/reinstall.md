# Reinstall runbook

How to take cp1 from "wipe it" back to a working member **without
losing the media library**. Current addresses, disk layout and cluster
facts live in [`../deployed-state.md`](../deployed-state.md); this file
is only the procedure.

> ⚠️ **Never run a bare `talosctl reset`.** It wipes the whole disk
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
