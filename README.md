# Talos Cluster Configuration

Declarative [Talos Linux](https://www.talos.dev/) cluster configuration with PXE boot provisioning.

Machines PXE boot via [siderolabs/booter](https://github.com/siderolabs/booter) and automatically receive their configuration from a config server that composes four layers of patches using `talosctl machineconfig patch`.

## Structure

```
talos/                           # Talos cluster configuration
  base/                          #   Role templates (shared boilerplate)
    controlplane.yaml
    worker.yaml
  clusters/<name>/               #   Cluster identity + secrets
    cluster.yaml                 #     name, endpoint, certSANs
    secrets.yaml.age             #     encrypted cluster crypto material
  hardware/<type>.yaml           #   Hardware-specific config (disk, NIC, installer image)
  machines/<mac>/                #   Per-machine config
    meta.yaml                    #     ip, base config, patch list
    patch.yaml                   #     machine-specific overrides (optional)
  talosconfig.age                #   Encrypted talosctl admin credentials (all clusters)

config-server/                   # Go PXE boot config server (talos machinery)

k8s/                             # Kubernetes manifests (TODO)
```

Each machine is defined by a MAC address mapped to a composition of these four layers:

| Layer | What it controls | Example |
|-------|-----------------|---------|
| **Role** | Base template — controlplane or worker | `base/controlplane.yaml` |
| **Cluster** | Cluster identity, endpoint, secrets | `clusters/homelab/cluster.yaml` (controlplane) · `clusters/homelab/worker-cluster.yaml` + `worker-secrets.yaml` (worker) |
| **Hardware** | Disk, installer image, NIC config | `hardware/minipc.yaml` |
| **Machine** | Per-machine overrides | `machines/b0-41-6f-15-3b-8f/patch.yaml` |

Patches are applied in order via `talosctl machineconfig patch`, matching the standard Talos strategic merge patch format. A patch may also be an RFC6902 JSON patch list, which is the only way to *remove* a key a base template sets.

**Workers take a different cluster layer.** Talos rejects a worker config carrying etcd config or any issuing CA key, so `clusters/<name>/cluster.yaml` + `secrets.yaml` are controlplane-only; workers use the `worker-` pair. Regenerate `worker-secrets.yaml` with the `yq` one-liner in its own header whenever `secrets.yaml` changes.

**The role templates deliberately set no install disk.** The hardware layer must state `machine.install.disk` (or a `diskSelector`), otherwise validation fails — a loud error instead of the generated default `/dev/sda`, which on at least one of these machines is a USB boot stick.

## machines/

Each machine gets a directory named by MAC address (dashes for colons):

```
machines/b0-41-6f-15-3b-8f/
  meta.yaml       # metadata: name, uuid, base config, patch list
  patch.yaml      # machine-specific Talos patch (optional)
```

`meta.yaml`:
```yaml
name: cp1                  # mesh name -> cp1.mesh.internal (member cert, apid SAN)
uuid: 37a1f6ed-…           # SMBIOS UUID: the durable KMS unseal allowlist
diskEncryption: true       # inject KMS + recovery-passphrase LUKS config at serve time
config: base/controlplane.yaml
patches:
  - clusters/homelab/cluster.yaml
  - clusters/homelab/secrets.yaml
  - hardware/minipc.yaml
```

`name:` is the machine's mesh name: the member cert's name caveat, the
`<name>.mesh.internal` apid certSAN, and what the name map resolves on
the irohup tun. It defaults to the MAC with dashes. No address is
declared — members are dialed by key (Mesh v3, ADR-0016); the LAN
address is the machine's own business (`patch.yaml`, or DHCP).

`patch.yaml` is a standard Talos strategic merge patch — usable directly with `talosctl machineconfig patch`.

## Usage

### Prerequisites

Enter the dev shell (provides `talosctl`, `kubectl`, `k9s`, `age`, `config-server`):

```bash
nix develop --impure  # or use direnv; --impure is required by devenv (see flake.nix header)
```

Secrets are automatically decrypted on shell entry.

### PXE boot provisioning

Start the config server and PXE booter:

```bash
# Terminal 1: config server
nix run .#config-server

# Terminal 2: PXE booter
docker run --rm --network host \
  ghcr.io/siderolabs/booter:v0.3.0 \
  --extra-kernel-args "talos.config=http://<your-ip>:8080/config?mac=\${mac}"
```

Machines PXE boot, fetch their composed config by MAC address, and install to disk.

### After first boot

```bash
# Bootstrap etcd on the first controlplane node
talosctl bootstrap

# Get kubeconfig
talosctl kubeconfig
```

### Day-to-day management

The devshell sets `TALOSCONFIG` automatically, so `talosctl` works with full shell completions and no extra flags:

```bash
talosctl dashboard       # TUI dashboard
talosctl service         # list services
talosctl dmesg           # kernel logs
talosctl logs kubelet    # service logs
talosctl health          # cluster health check
talosctl kubeconfig      # fetch kubeconfig
talosctl reboot          # reboot node
```

Endpoints and nodes are configured in the talosconfig file (`talos/talosconfig`).

### Applying config changes

Edit patches, then push to machines:

```bash
# Apply to all machines
nix run .#apply

# Apply to a specific machine
nix run .#apply -- "b0:41:6f:15:3b:8f"
```

This composes `base + patches` via `talosctl machineconfig patch` and pushes with `talosctl apply-config`.

### Adding a new machine

The machine must be **declared in git and the hub redeployed before it can
fetch a config** — the hub serves `talos/machines/` as baked into its
image, so an undeclared MAC gets a 404 no matter how you approve it.
Approving first is wasted effort (the token is only burned on a
successful serve, so nothing is lost but time).

A machine sitting at the device-flow prompt has **no apid** — port 50000
is refused, so you cannot inspect its disks first. Read its identity out
of the hub's log instead, which needs no wallet and no approval:

```bash
flyctl logs -a marnyg-talos-config --no-tail | grep "device auth started"
# device auth started: user_code=… identity=map[mac:… serial:… uuid:…]
```

1. Hardware layer, if this hardware type is new — install disk (**required**;
   the role templates set none) and the factory installer image, which must
   carry the `p0agent` extension (`talos/extensions/`) or the node never
   joins the identity plane:
   ```yaml
   # hardware/new-hw.yaml
   machine:
     install:
       disk: /dev/nvme0n1   # verify! /dev/sda may be a USB boot stick
       image: factory.talos.dev/installer/<schematic>:v1.12.6
   ```
   If the disk is genuinely unknown, use a `diskSelector` sized to **fail
   safe** (a miss aborts the install and wipes nothing; a too-loose match
   can eat a data disk), then pin the device node after first boot.

2. Declare the machine — directory named by MAC with dashes:
   ```yaml
   # machines/aa-bb-cc-dd-ee-ff/meta.yaml
   name: w2                 # mesh name -> w2.mesh.internal
   uuid: <from the log above>  # record BEFORE first boot: makes the KMS
                              # allowlist durable instead of relying on the
                              # session-scoped "sealed this lifetime" grace
   diskEncryption: true
   config: base/worker.yaml
   patches:
     - clusters/homelab/worker-cluster.yaml   # worker- pair, not cluster.yaml
     - clusters/homelab/worker-secrets.yaml
     - hardware/new-hw.yaml
   ```

3. Optional `patch.yaml` for per-machine overrides — partition geometry,
   and a pinned hostname (recommended: Talos' generated names are *not*
   stable across reinstalls, so a wipe otherwise strands a NotReady node
   object):
   ```yaml
   machine:
     network:
       hostname: w2
   ```

4. Validate the composition before deploying — cheap, and catches
   role/layer mismatches that would otherwise surface as a refused serve:
   ```bash
   cd talos && talosctl machineconfig patch base/worker.yaml \
     --patch @clusters/homelab/worker-cluster.yaml \
     --patch @clusters/homelab/worker-secrets.yaml \
     --patch @hardware/new-hw.yaml -o /tmp/new.yaml \
     && talosctl validate -c /tmp/new.yaml -m metal
   ```

5. Commit, then deploy the hub (see [Deploying the hub](#deploying-the-hub)).
   **The deploy re-seals the hub**, so unseal
   at [`/status`](https://marnyg-talos-config.fly.dev/status) with the
   wallet afterwards (two signatures: master + speak-as) — config
   serving, KMS and the identity plane are all down until you do.

6. The machine restarts its device flow on its own (codes expire after
   10 minutes). **Approve it on `/status`** — check the displayed
   uuid/mac/serial against the machine you think you are approving.
   It then installs, reboots, and its agent enrolls itself with the
   boot token in its served config (ADR-0015); it shows up under
   "Members" on `/status` once it beats.

Subsequent config changes are `nix run .#apply` (over the identity
plane, hub-composed — never composed locally, which would strip
serve-time identity).

## Deploying the hub

The fly image is built by nix (`fly/image.nix`), not by fly's remote
builder: `config-server` is cgo against `libiroh_ffi.a` since the hub
bound its own iroh endpoint (`e8d`, ADR-0024), and the Rust pin lives
in one place (`iroh-go/nix/sources.nix`). The image is x86_64-linux, so
from a laptop the build runs in a linux nix store over ssh:

```bash
HUB_BUILDER=mar@nixos fly/deploy.sh     # build + push on the box, fly deploy --image
fly/deploy.sh --no-deploy               # on a linux host: build + push only
```

`.github/workflows/hub-image.yml` builds the same derivation on every
push (and pushes it on `workflow_dispatch` with `FLY_API_TOKEN`), so a
laptop without a builder is never stuck. A bare `fly deploy` has no
`[build]` section to work from and fails on purpose. The deploy
**re-seals the hub** — sign both proposals at `/status` afterwards.

Everyday `go test ./...` in `config-server/` stays C-free: the iroh
binding sits behind the `iroh` build tag (`hubiroh.go` / `hubiroh_stub.go`);
`nix build .#config-server-bin` runs the tagged suite, including the
hub beat over a local `iroh-relay`.

## Secret management

Secrets are encrypted with [age](https://github.com/FiloSottile/age) using your SSH key (`~/.ssh/id_ed25519`).

```bash
nix run .#decrypt-secrets    # Decrypt all .age files
nix run .#encrypt-secrets    # Re-encrypt after changes
nix run .#edit-secrets -- clusters/homelab/secrets.yaml  # Edit in place
```

Only `.age` files are committed to git. Decrypted files are gitignored.
