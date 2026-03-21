# Talos Cluster Configuration

Declarative [Talos Linux](https://www.talos.dev/) cluster configuration with PXE boot provisioning.

Machines PXE boot via [siderolabs/booter](https://github.com/siderolabs/booter) and automatically receive their configuration from a config server that composes four layers of patches using `talosctl machineconfig patch`.

## Structure

```
base/                            # Role templates (shared boilerplate)
  controlplane.yaml
  worker.yaml

clusters/<name>/                 # Cluster identity + secrets
  cluster.yaml                   #   name, endpoint, certSANs
  secrets.yaml.age               #   encrypted cluster crypto material
  talosconfig.age                #   encrypted talosctl admin credentials

hardware/<type>.yaml             # Hardware-specific config (disk, NIC, installer image)

machines/<mac>.yaml              # Per-machine overrides (optional)

machines.json                    # Maps MAC address → role + patches
```

Each machine is defined by a MAC address mapped to a composition of these four layers:

| Layer | What it controls | Example |
|-------|-----------------|---------|
| **Role** | Base template — controlplane or worker | `base/controlplane.yaml` |
| **Cluster** | Cluster identity, endpoint, secrets | `clusters/homelab/cluster.yaml` |
| **Hardware** | Disk, installer image, NIC config | `hardware/minipc.yaml` |
| **Machine** | Per-machine overrides | `machines/b0-41-6f-15-3b-8f.yaml` |

Patches are applied in order via `talosctl machineconfig patch`, matching the standard Talos strategic merge patch format.

## machines.json

Maps MAC addresses to a base config, IP, cluster, and an ordered list of patches:

```json
{
  "b0:41:6f:15:3b:8f": {
    "ip": "192.168.2.177",
    "config": "base/controlplane.yaml",
    "cluster": "homelab",
    "patches": [
      "clusters/homelab/cluster.yaml",
      "clusters/homelab/secrets.yaml",
      "hardware/minipc.yaml",
      "machines/b0-41-6f-15-3b-8f.yaml"
    ]
  }
}
```

The `apply` command reads this file to compose and push configs.

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

Endpoints and nodes are configured in the talosconfig file (`clusters/homelab/talosconfig`).

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

2. Optionally create a machine override:
   ```yaml
   # machines/aa-bb-cc-dd-ee-ff.yaml
   machine:
     network:
       hostname: worker-1
   ```

3. Add the MAC to `machines.json`:
   ```json
   "aa:bb:cc:dd:ee:ff": {
     "ip": "192.168.2.x",
     "config": "base/worker.yaml",
     "cluster": "homelab",
     "patches": [
       "clusters/homelab/cluster.yaml",
       "clusters/homelab/secrets.yaml",
       "hardware/new-hw.yaml"
     ]
   }
   ```

4. PXE boot the machine, then `nix run .#apply` to push updates.

## Secret management

Secrets are encrypted with [age](https://github.com/FiloSottile/age) using your SSH key (`~/.ssh/id_ed25519`).

```bash
nix run .#decrypt-secrets    # Decrypt all .age files
nix run .#encrypt-secrets    # Re-encrypt after changes
nix run .#edit-secrets -- clusters/homelab/secrets.yaml  # Edit in place
```

Only `.age` files are committed to git. Decrypted files are gitignored.
