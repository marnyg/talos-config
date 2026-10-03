# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Repo Is

Declarative Talos Linux Kubernetes cluster configuration. Machines are provisioned via PXE boot and configured through a four-layer strategic merge patch system using `talosctl machineconfig patch`.

**Monorepo layout (decision `talos-config-5w1`, ADR-0020).** This repo
is a monorepo built around the **sovereign-actor protocol** — a reusable
core (actors as keypair+wallet, authority as one delegation-cert
primitive) with talos-config as its first consumer:

- `protocol/` — the protocol as its own Go module
  (`github.com/marnyg/talos-config/protocol`), with no dependency on
  config-server, Talos, fly, or nebula. config-server imports it via a
  `replace` directive when Mesh v3 Phase 1 wires it in. Its own docs
  sub-scope lives at `protocol/docs/` (goals/invariants/domain-model
  independent of any deployment; the design sketch
  `sovereign-actor-protocol.md`; ADRs starting at 0001).
- `config-server/`, `talos/`, `k8s/` — the talos deployment, the
  protocol's N=1 single-sovereign consumer. Root docs in `docs/` stay
  authoritative for the deployment.

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
talos.config.oauth.extra_variable=uuid
talos.config.oauth.extra_variable=mac
talos.config.oauth.extra_variable=serial
```

(`extra_variable` must be repeated per variable — Talos rejects a
comma-separated list with "unsupported variable name".)

Talos hits `POST /device/code` (sending its hardware identity), prints a user code on the console, and polls `POST /token`. A human approves the machine on the `/status` dashboard (SIWE session login; `GET /verify` redirects there). Tokens are **single-use** and **bound to the MAC** captured at device-auth time — one approval serves exactly one config, to exactly that machine. All flow state is in-memory; a server restart just restarts the flow on the machine console.

Viewing `/status` requires a session (wallet login, or the admin token as break-glass). Each approval action is authorized independently of the session by either (in order of preference):

1. **Wallet signature** (`--admin-address 0x...,0x...`) — the admin signs a canonical message (EIP-191 `personal_sign`) binding action + user code + a per-request nonce. Works via browser wallet (in-page button) or headless via `cast wallet sign` + paste. Verification is offline signature recovery against the allowlist — no OIDC provider, no chain RPC (EOA only, by design).
2. **Admin token** (`CONFIG_SERVER_ADMIN_TOKEN` env) — break-glass fallback.

At least one must be configured when `--require-auth` is on.

### Mesh v3 — the identity plane (no WireGuard, no nebula)

The hub runs an embedded iroh relay and issues member certs rooted in
the owner's wallet (ADR-0017/0022/0024). Each member holds a **Kit**:
member cert (`cav.name`, `cav.groups`, 90 d), the renewal-beat grant,
`invoke` grants compiled from `talos/mesh-policy-v3.yaml` (7 d, renewed
daily), and a self-issued `reach-me-at`. Machines enroll at boot via a
single-use token; devices via `irohup` (wallet signs) or the RFC 8628
device flow on `/status`. Names `<svc>.<member>.mesh.internal` resolve
on each device from the witnessed name map to fake IPs in `198.18/15`
(`config-server/{fakeip,meshtun}`; utun on macOS, `VpnService` on
Android). Remote members relay through the hub; same-LAN members
hole-punch direct. Pods reach cluster services by ClusterIP, never by
a member's mesh address.

Authoritative detail: `docs/technical/deployed-state.md` (Mesh section),
`docs/mesh-v3-iroh.md` (plan + phase log), ADR-0017 (policy recipe),
ADR-0024 (name witnessing).

### Recovery USB

If PXE isn't available, build a boot stick that replicates the PXE
flow (device flow → wallet approve → install, `wipe: false`):

```bash
curl -s -X POST --data-binary @- https://factory.talos.dev/schematics <<'EOF'
customization:
  extraKernelArgs:
    - talos.config=https://marnyg-talos-config.fly.dev/config?mac=${mac}
    - talos.config.oauth.client_id=talos-pxe
    - talos.config.oauth.extra_variable=uuid
    - talos.config.oauth.extra_variable=mac
    - talos.config.oauth.extra_variable=serial
EOF
# → {"id":"<schematic>"} ; then:
curl -sLo talos-usb.iso "https://factory.talos.dev/image/<schematic>/<talos-version>/metal-amd64.iso"
sudo dd if=talos-usb.iso of=/dev/<usb> bs=4M oflag=sync status=progress
```

Note: a fresh install always recreates EPHEMERAL (`wipe: false`
protects the disk layout, not EPHEMERAL contents) — etcd and
`/var/media` are lost; ArgoCD + SealedSecrets rebuild everything from
git after auto/manual bootstrap.

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


<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:ca08a54f -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

## Session Completion

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   bd dolt push
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds
<!-- END BEADS INTEGRATION -->
