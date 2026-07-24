# Handover — next session

_Last updated: 2026-07-24 (evening, post reprovision-day). Read alongside `docs/vision.md` (why) — this file is the what/where/next._

## Where things stand

### The cluster — rebuilt encrypted, tunnel-native

- Single CP node `b0:41:6f:15:3b:8f`, Talos v1.12.6, k8s v1.32.3,
  reprovisioned 2026-07-24 with **LUKS2 on STATE+EPHEMERAL**.
- **Cluster endpoint is the node's derived tunnel IP**
  `https://10.99.0.54:6443` (decision: cluster correctness must not
  depend on local hardware state — DHCP handed out four different
  leases in one day and none of it mattered). apiServer certSAN
  matches. LAN IP is cosmetic; don't record it anywhere.
- **First real auto-Bootstrap fired and validated** (etcd waited ×2 →
  Bootstrap accepted → running). Unattended reboot recovery ~20s.
- ArgoCD re-synced the app stack from git unaided. Media pods were
  still crash-settling at session end — check `kubectl get pods -n
  media` before assuming anything is broken.
- Admin access is **tunnel-only** now: `talos/talosconfig` (local,
  gitignored) points at 10.99.0.54; kubeconfig via `talosctl
  kubeconfig` gets the tunnel endpoint natively; NodePorts (Jellyfin
  :30096 etc.) serve on wg0. All require the laptop WG peer to be up.

### Disk encryption posture (decision, recorded + closed)

Slot 0 = KMS (fly, WAN, per-boot) — currently **dormant in practice**:
early-boot DNS isn't up when STATE unlocks, so every boot so far used
slot 1. Slot 1 = static derived passphrase, persisted in plaintext
META by Talos. Accepted trade-off: encryption protects disk
disposal/RMA only; theft protection and real revocation would need
KMS-only (break-glass tooling for slot-0 blobs doesn't exist — build
it before ever dropping slot 1). Consequence: sealed fly server does
NOT block reboots, only provisioning — task `19a4c316` (seal window)
is low-stakes until this changes.

### Admin over the tunnel — SHIPPED & validated live (task `b79e0570`)

- `wgstack/`: vendored trimmed netstack TUN with **IPv4 forwarding**
  (upstream keeps the stack unexported, same story as the fly bind).
  The hub now routes peer↔peer. E2E test: real hub + two userspace
  peers over loopback UDP, TCP straight through.
- **Admin peers**: named identities (env `WG_ADMIN_PEERS = "laptop"`
  in fly.toml), keys/IPs HKDF-derived from new frozen v1 domains
  (`admin-key/`, `admin-addr/` — vectors pinned). No registry.
- Laptop config: `wgping -admin laptop -sig <unseal-sig> -wgquick
  -endpoint 213.188.219.215:51820`. Laptop = `10.99.0.207`.
- **MTU gotcha (cost an hour): the hub netstack runs at 1280.** A
  client at 1420 blackholes TLS-sized packets mid-handshake while
  pings/hellos pass. wgquick output now emits `MTU = 1240`; any
  hand-rolled peer config must too.
- **kube-proxy nftables gotcha**: NodePorts default to the primary
  node IP only (unlike iptables mode). Fixed in cluster.yaml
  (`proxy.extraArgs.nodeport-addresses: 0.0.0.0/0`) for future
  provisions AND live-patched into the kube-proxy DaemonSet (Talos
  renders bootstrap manifests once; config changes don't reconcile
  them post-bootstrap).
- Forwarding hub is also the missing piece multi-node needed: a second
  node can now reach the endpoint through the hub.

### The config server (fly app marnyg-talos-config)

All previous slices still live: device flow, wallet approval, SIWE
status page, sealed-server unseal, KMS gRPC (:8443, h2_backend under
http_options — the exact nesting matters), auto-bootstrap.
**Deployed image is at `5bb5a6f`; repo is ahead** (`346c877`: wgping
MTU, cluster.yaml proxy args, meta.yaml tunnel ip) — rides the next
deploy, nothing urgent. Every deploy re-seals; unseal at /verify.
Secrets on fly: only `AGE_KEY` (task `9316194f`… no — see task list:
wallet-derived age identity retires it, queued next).

### Root of trust (unchanged)

Admin wallet `0xf568…9406` signs `talos-config/wg/master/v1` →
HKDF master (memory only) → server WG key, machine keys, admin peer
keys, tunnel IPs, KMS seal keys, recovery passphrases. The signature
IS the fleet master. Sign it nowhere except /verify or offline
`cast wallet sign`.

## Next session — pick up

1. **Wallet-derived age identity** (task queued, refs `da2e069d`):
   HKDF(master, `age/v1`) → X25519 age identity; commit the public
   recipient; decrypt secrets **at unseal time** instead of
   entrypoint (strictly better: today plaintext sits in tmpfs while
   sealed); retire `AGE_KEY`; keep ssh key as break-glass recipient.
   One deploy, one unseal ceremony, zero secrets at rest on fly.
2. Parked: SideroLink v3 (task `9da98ad2`), endpoint DNS/VIP thread
   (`9b5b204d` — largely absorbed by the tunnel-endpoint decision;
   re-read before acting), seal-window thread (`19a4c316`, teeth
   pulled by the slot-1 decision).

## Gotchas that cost time (new this session)

- Hub netstack MTU 1280: clamp every WG client to ≤1240 or TLS
  blackholes. Symptom: ping works, small HTTP works, any TLS
  handshake times out.
- kube-proxy nftables mode ≠ iptables defaults: NodePorts bind
  primary IP only.
- Talos bootstrap manifests (kube-proxy DS etc.) render once at
  bootstrap; `cluster.proxy` config changes need a manual DS patch or
  `talosctl upgrade-k8s` on a live cluster.
- `fly logs` replays old buffered history — filter by timestamp
  before drawing conclusions (nearly misdiagnosed a seal state from
  stale lines).
- Talos KMS slot ordering: install-time seal/unseal happens during
  volume creation; boot-time unlock is a separate path that races
  early-boot DNS. `encryptionSlot` in volumestatus tells you which
  slot actually opened the volume.

## Loose ends

- [ ] Media stack pods still settling post-rebuild (nzbget/radarr
      CreateContainerConfigError at session end — likely sealed-secret
      timing; recheck before debugging)
- [ ] Deployed fly image behind repo (`5bb5a6f` vs `346c877`) — next
      deploy syncs it
- [ ] DHCP reservation: obsolete as a correctness issue (endpoint
      decision); LAN IP only matters for LAN-device media access (TV)
- [ ] `k8s/MIGRATION.md` untracked — user said leave it
- [ ] `.claude/` untracked in repo root — gitignore?
- [ ] Old kubeconfigs/talosconfigs pointing at 10.0.0.20 anywhere
      outside the repo will fail — regenerate via `talosctl
      kubeconfig` over the tunnel

## Taskwarrior map

`task +repo_5efa11ff status:pending list`. Closed this session:
sketch `c8fa867d` (control channel v2 — the whole arc), task 30
(reprovision day), decisions: endpoint/local-hardware-independence
(task 32-old), slot-1-encryption-posture (task 33-old). Shipped &
awaiting close confirm: admin-over-tunnel task `b79e0570` + its
origin thread `0b7713a4`. Queued: wallet age identity, SideroLink v3.
