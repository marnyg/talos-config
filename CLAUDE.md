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
nix run .#edit-secrets -- talos/clusters/homelab/secrets.yaml
```

## Architecture: Four-Layer Config Composition

Every machine's config is built by patching a base template with layers:

```
base role  →  cluster  →  hardware  →  machine override
```

| Layer | Path | Purpose |
|-------|------|---------|
| Role | `talos/base/controlplane.yaml`, `talos/base/worker.yaml` | Full Talos config templates with shared boilerplate, secrets stripped to `""` |
| Cluster | `talos/clusters/<name>/cluster.yaml` + `secrets.yaml` | Cluster identity, endpoint, certSANs, crypto material |
| Hardware | `talos/hardware/<type>.yaml` | Disk, installer image, NIC config |
| Machine | `talos/machines/<mac>/patch.yaml` | Per-machine overrides (optional) |

Patches are standard Talos strategic merge patches — the same format used by `talosctl machineconfig patch` and `talosctl gen config --config-patch`.

## talos/machines/<mac>/

Each machine is a directory named by MAC address (dashes instead of colons) containing:
- `meta.yaml` — ip, base config, ordered patch list
- `patch.yaml` — optional machine-specific Talos strategic merge patch (valid for direct use with `talosctl machineconfig patch`)

The `apply` command and `config-server` scan `talos/machines/` to discover all machines. All paths in `meta.yaml` are relative to `talos/`.

## Secrets

Cluster secrets (CAs, tokens, keys) are in `talos/clusters/<name>/secrets.yaml`, encrypted with age using `~/.ssh/id_ed25519`. Admin credentials are in `talos/talosconfig` (supports multiple contexts for multiple clusters). Only `.age` files are committed. The devshell auto-decrypts on entry and sets `TALOSCONFIG`.

## Sealed Secrets (Kubernetes-level secrets)

Kubernetes secrets (e.g. NNTP credentials) use Bitnami Sealed Secrets with a pre-provisioned key pair. The cluster has its own identity — you only need the public cert to add or rotate secrets.

**Trust chain:**
```
~/.ssh/id_ed25519 (root of trust)
  → decrypts sealed-secrets.yaml.age (contains TLS key pair)
    → Talos inlineManifest provisions key pair into cluster at boot
      → Sealed Secrets controller uses key pair to decrypt SealedSecrets
        → ArgoCD syncs SealedSecret CRDs from k8s/apps/, controller creates Secrets
```

**Public cert:** `talos/clusters/homelab/sealed-secrets.crt`

**Adding/rotating a secret:**
```bash
# Create a plain secret YAML (DO NOT commit this)
kubectl create secret generic my-secret -n media \
  --from-literal=key=value --dry-run=client -o yaml > /tmp/secret.yaml

# Encrypt with cluster's public cert
kubeseal --cert talos/clusters/homelab/sealed-secrets.crt \
  --format yaml < /tmp/secret.yaml > k8s/apps/myapp/sealed-secret.yaml

# Commit the SealedSecret (safe — only decryptable by this cluster)
git add k8s/apps/myapp/sealed-secret.yaml && git commit && git push
# ArgoCD syncs → controller decrypts → Secret available in cluster

rm /tmp/secret.yaml  # clean up plaintext
```

**Key files:**
| File | Encrypted | Purpose |
|------|-----------|---------|
| `talos/clusters/homelab/sealed-secrets.crt` | No (public) | Cert for `kubeseal --cert` |
| `talos/clusters/homelab/sealed-secrets.yaml.age` | Yes (age) | TLS key pair, provisioned into cluster at boot |
| `k8s/apps/*/sealed-secret.yaml` | Yes (RSA) | SealedSecret CRDs, decrypted in-cluster |

## Config Server (talos/serve-config.py)

Python HTTP server that receives `GET /config?mac=<mac>`, looks up the machine in `machines.json`, runs `talosctl machineconfig patch` to compose base + all patches, and returns the merged config. Used during PXE boot via the `talos.config` kernel argument.

## Kubernetes Apps (k8s/apps/)

ArgoCD watches `k8s/apps/` (recursive, auto-sync with prune + self-heal) from the `main` branch of `github.com/marnyg/talos-config`. All manifests pushed to `main` are automatically deployed.

**Media stack** (namespace: `media`):
| Service | NodePort | Role |
|---------|----------|------|
| Jellyfin | 30096 | Media streaming |
| Sonarr | 30989 | TV management |
| Radarr | 30878 | Movie management |
| NZBget | ClusterIP | Usenet downloader |
| Transmission | ClusterIP (+ 31413 peer) | Torrent client |
| Jackett | ClusterIP | Indexer aggregator |

Storage is hostPath PVs at `/var/media/{tv,movies,downloads}`.

## Do not sign commit messages as claude
