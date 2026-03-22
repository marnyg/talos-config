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
  serve-config.py                #   PXE boot config server

k8s/                             # Kubernetes manifests (TODO)
```

Each machine is defined by a MAC address mapped to a composition of these four layers:

| Layer | What it controls | Example |
|-------|-----------------|---------|
| **Role** | Base template — controlplane or worker | `base/controlplane.yaml` |
| **Cluster** | Cluster identity, endpoint, secrets | `clusters/homelab/cluster.yaml` |
| **Hardware** | Disk, installer image, NIC config | `hardware/minipc.yaml` |
| **Machine** | Per-machine overrides | `machines/b0-41-6f-15-3b-8f.yaml` |

Patches are applied in order via `talosctl machineconfig patch`, matching the standard Talos strategic merge patch format.

## machines/

Each machine gets a directory named by MAC address (dashes for colons):

```
machines/b0-41-6f-15-3b-8f/
  meta.yaml       # metadata: ip, base config, patch list
  patch.yaml      # machine-specific Talos patch (optional)
```

`meta.yaml`:
```yaml
ip: 192.168.2.177
config: base/controlplane.yaml
patches:
  - clusters/homelab/cluster.yaml
  - clusters/homelab/secrets.yaml
  - hardware/minipc.yaml
```

`patch.yaml` is a standard Talos strategic merge patch — usable directly with `talosctl machineconfig patch`.

## Usage

### Prerequisites

Enter the dev shell (provides `talosctl`, `kubectl`, `k9s`, `age`, `config-server`):

```bash
nix develop  # or use direnv
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

1. Create a hardware patch if the hardware type is new:
   ```yaml
   # hardware/new-hw.yaml
   machine:
     install:
       disk: /dev/sda
   ```

2. Create the machine directory:
   ```bash
   mkdir machines/aa-bb-cc-dd-ee-ff
   ```

3. Create `meta.yaml`:
   ```yaml
   # machines/aa-bb-cc-dd-ee-ff/meta.yaml
   ip: 192.168.2.x
   config: base/worker.yaml
   patches:
     - clusters/homelab/cluster.yaml
     - clusters/homelab/secrets.yaml
     - hardware/new-hw.yaml
   ```

4. Optionally create `patch.yaml` for machine-specific overrides:
   ```yaml
   # machines/aa-bb-cc-dd-ee-ff/patch.yaml
   machine:
     network:
       hostname: worker-1
   ```

5. PXE boot the machine, then `nix run .#apply` to push updates.

## Secret management

Secrets are encrypted with [age](https://github.com/FiloSottile/age) using your SSH key (`~/.ssh/id_ed25519`).

```bash
nix run .#decrypt-secrets    # Decrypt all .age files
nix run .#encrypt-secrets    # Re-encrypt after changes
nix run .#edit-secrets -- clusters/homelab/secrets.yaml  # Edit in place
```

Only `.age` files are committed to git. Decrypted files are gitignored.
