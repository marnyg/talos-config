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

## Config Server (config-server/)

Go HTTP server that receives `GET /config?mac=<mac>`, scans `talos/machines/` for the machine, and composes base + all patches in-process using the Talos machinery library (`configpatcher` — the same code path as `talosctl machineconfig patch`). Used during PXE boot via the `talos.config` kernel argument. Built via `nix build .#config-server-bin`; `nix run .#config-server` wraps it with the repo root.

### OAuth device-flow authentication (optional)

With `--require-auth` (needs `CONFIG_SERVER_ADMIN_TOKEN` env), `/config` requires a bearer token obtained via the OAuth2 device flow (RFC 8628). Machines boot with:

```
talos.config=http://<server>:8080/config?mac=${mac}
talos.config.oauth.client_id=talos-pxe
talos.config.oauth.extra_variable=uuid,mac,serial
```

Talos hits `POST /device/code` (sending its hardware identity), prints a user code on the console, and polls `POST /token`. A human approves the machine on `GET /verify`. Tokens are **single-use** and **bound to the MAC** captured at device-auth time — one approval serves exactly one config, to exactly that machine. All flow state is in-memory; a server restart just restarts the flow on the machine console.

Approval on `/verify` is authorized by either (in order of preference):

1. **Wallet signature** (`--admin-address 0x...,0x...`) — the admin signs a canonical message (EIP-191 `personal_sign`) binding action + user code + a per-request nonce. Works via browser wallet (in-page button) or headless via `cast wallet sign` + paste. Verification is offline signature recovery against the allowlist — no OIDC provider, no chain RPC (EOA only, by design).
2. **Admin token** (`CONFIG_SERVER_ADMIN_TOKEN` env) — break-glass fallback.

At least one must be configured when `--require-auth` is on.

### WireGuard control channel, tunnel DNS, and admin devices

The server runs a userspace WireGuard hub (fly UDP :51820). All key
material is HKDF-derived from the wallet unseal signature — no peer
registry, no key state. Machines get their `wg0` injected into served
configs; the hub forwards peer↔peer (admin laptop → node).

**Tunnel DNS**: the hub answers A queries on `10.99.0.1:53` for
`<name>.talos.wg` — `hub`, each machine (`name:` in `meta.yaml`,
MAC-with-dashes fallback), and each admin peer. Zone is derived at
unseal; out-of-zone queries are REFUSED (split-DNS). The DNS name is
also added to machine certSANs, so `talosctl -e cp1.talos.wg` works.
Disable/override with `--wg-dns-domain`.

**Connecting an admin device** (must be declared in `WG_ADMIN_PEERS`):

```bash
wgup                 # enroll (wallet signs a device-bound nonce), then wg-quick up
wgup -down           # disconnect
wgup -name phone -print   # enroll another device, just print the config path
```

Enrollment (`GET`/`POST /wg/enroll`) never touches the fleet master:
the wallet signs an ordinary single-use challenge, the server returns
the derived wg-quick config over TLS. The master signature is only
ever signed at `/verify` (unseal) or used offline with
`wgping -sig <sig> …` as break-glass.

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
