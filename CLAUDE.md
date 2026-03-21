# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Repo Is

Declarative Talos Linux Kubernetes cluster configuration. Machines are provisioned via PXE boot and configured through a four-layer strategic merge patch system using `talosctl machineconfig patch`.

## Key Commands

```bash
nix develop                  # Enter devshell (or use direnv)
talosctl service             # TALOSCONFIG is set automatically, full completions
talosctl dashboard           # Endpoints/nodes configured in talosconfig
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
| Cluster | `clusters/<name>/cluster.yaml` + `secrets.yaml` | Cluster identity, endpoint, certSANs, crypto material |
| Hardware | `hardware/<type>.yaml` | Disk, installer image, NIC config |
| Machine | `machines/<mac>.yaml` | Per-machine overrides (optional) |

Patches are standard Talos strategic merge patches — the same format used by `talosctl machineconfig patch` and `talosctl gen config --config-patch`.

## machines/<mac>/

Each machine is a directory named by MAC address (dashes instead of colons) containing:
- `meta.yaml` — ip, base config, ordered patch list
- `patch.yaml` — optional machine-specific Talos strategic merge patch (valid for direct use with `talosctl machineconfig patch`)

The `apply` command and `config-server` scan `machines/` to discover all machines.

## Secrets

Cluster secrets (CAs, tokens, keys) are in `clusters/<name>/secrets.yaml`, encrypted with age using `~/.ssh/id_ed25519`. Admin credentials are in `talosconfig` at the repo root (supports multiple contexts for multiple clusters). Only `.age` files are committed. The devshell auto-decrypts on entry and sets `TALOSCONFIG`.

## Config Server (serve-config.py)

Python HTTP server that receives `GET /config?mac=<mac>`, looks up the machine in `machines.json`, runs `talosctl machineconfig patch` to compose base + all patches, and returns the merged config. Used during PXE boot via the `talos.config` kernel argument.

## Do not sign commit messages as claude
