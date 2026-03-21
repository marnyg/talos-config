# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Repo Is

Declarative Talos Linux Kubernetes cluster configuration. Machines are provisioned via PXE boot and configured through a four-layer strategic merge patch system using `talosctl machineconfig patch`.

## Key Commands

```bash
nix develop                  # Enter devshell (or use direnv)
tc service                   # talosctl wrapper — auto-resolves IP/talosconfig from machines.json
tc dmesg                     # With one machine, MAC is auto-selected
tc b0:41:6f:15:3b:8f logs kubelet  # With multiple machines, specify MAC
nix run .#apply              # Compose and push config to all machines
nix run .#apply -- "<mac>"   # Push to specific machine
nix run .#config-server      # HTTP server for PXE boot config delivery
nix run .#decrypt-secrets    # Decrypt .age files (also runs on shell entry)
nix run .#encrypt-secrets    # Re-encrypt after editing secrets
nix run .#edit-secrets -- clusters/homelab/secrets.yaml
```

## Architecture: Four-Layer Config Composition

Every machine's config is built by patching a base template with layers:

```
base role  →  cluster  →  hardware  →  machine override
```

| Layer | Path | Purpose |
|-------|------|---------|
| Role | `base/controlplane.yaml`, `base/worker.yaml` | Full Talos config templates with shared boilerplate, secrets stripped to `""` |
| Cluster | `clusters/<name>/cluster.yaml` | Cluster identity: name, endpoint, certSANs |
| Hardware | `hardware/<type>.yaml` | Disk, installer image, NIC config |
| Machine | `machines/<mac>.yaml` | Per-machine overrides (optional) |

Patches are standard Talos strategic merge patches — the same format used by `talosctl machineconfig patch` and `talosctl gen config --config-patch`.

## machines.json

Single source of truth mapping MAC addresses to their config composition. The `tc` wrapper, `apply` command, and `config-server` all read from this file.

```json
{
  "b0:41:6f:15:3b:8f": {
    "ip": "192.168.2.177",
    "config": "base/controlplane.yaml",
    "cluster": "homelab",
    "patches": ["clusters/homelab/cluster.yaml", "clusters/homelab/secrets.yaml", "hardware/minipc.yaml", "machines/b0-41-6f-15-3b-8f.yaml"]
  }
}
```

## Secrets

Cluster secrets (CAs, tokens, keys) are extracted from configs into `clusters/<name>/secrets.yaml`, encrypted with age using `~/.ssh/id_ed25519`. Only `.age` files are committed. The devshell auto-decrypts on entry. Talosconfig files (admin credentials) follow the same pattern.

## Config Server (serve-config.py)

Python HTTP server that receives `GET /config?mac=<mac>`, looks up the machine in `machines.json`, runs `talosctl machineconfig patch` to compose base + all patches, and returns the merged config. Used during PXE boot via the `talos.config` kernel argument.

## Do not sign commit messages as claude
