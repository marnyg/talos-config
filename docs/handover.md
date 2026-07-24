# Handover — next session

_Last updated: 2026-07-24 (end of day). Read alongside `docs/vision.md` (why) — this file is the what/where/next._

## The day in one paragraph

Reprovision day happened and then kept going: the CP node was wiped
and rebuilt with LUKS2 disks, the first real auto-Bootstrap fired, the
cluster endpoint moved off DHCP onto the node's derived tunnel IP
(after DHCP handed out four leases in one day), the laptop became a
WG peer with full talosctl/kubectl/NodePort access through a
forwarding hub, secrets moved to a wallet-derived age identity
decrypted at unseal, `AGE_KEY` was deleted — **fly now holds zero
secrets** — and the sealed-secrets controller (silently killed by the
Bitnami chart purge) was re-vendored and verified unsealing real
Secrets on the rebuilt cluster.

## Where things stand

### The cluster — encrypted, tunnel-native, fully healthy

- Single CP `b0:41:6f:15:3b:8f`, Talos v1.12.6, k8s v1.32.3.
  LUKS2 on STATE+EPHEMERAL. Endpoint `https://10.99.0.54:6443`
  (derived tunnel IP; LAN IP is cosmetic, don't record it).
- Media stack 6/6 Running; SealedSecrets (`newshosting`, `nzbgeek`)
  unseal correctly via the inlineManifest-provisioned key pair.
- Admin access is tunnel-only: laptop WG peer `10.99.0.207`
  (**move `/tmp/wg-talos.conf` somewhere permanent + chmod 600**),
  `talos/talosconfig` (local, gitignored) points at 10.99.0.54,
  NodePorts serve on wg0 (Jellyfin `10.99.0.54:30096`).

### The config server — zero secrets at rest

- `fly secrets list` is EMPTY. Everything derives from the wallet
  signature at unseal: WG keys (server/machine/admin), tunnel IPs,
  KMS seal keys, recovery passphrases, and now the **age identity**
  (`talos-config/age/v1/identity`, frozen, vectors pinned) that
  decrypts `clusters/**/*.age` into tmpfs at unseal time. An unseal
  that cannot decrypt the secrets fails loudly.
- Public recipient committed: `talos/age-recipient.txt` (derive:
  `wgping -age-recipient -sig <unseal-sig>`). ssh key remains
  break-glass recipient (verified). Legacy fly deploy key deleted.
- Hub netstack (`wgstack/`) forwards peer↔peer; admin peers declared
  via `WG_ADMIN_PEERS` (fly.toml), keys from frozen `admin-key/` /
  `admin-addr/` domains. Laptop config:
  `wgping -admin laptop -sig <sig> -wgquick -endpoint 213.188.219.215:51820`.
- Deployed image current with repo as of `392e4b5`.

### Encryption posture (decision, closed)

Slot 0 KMS (dormant at boot — early-boot DNS loses the race every
time so far), slot 1 static passphrase in plaintext META. Accepted:
encryption = disk disposal/RMA protection only. Sealed fly server
does NOT block reboots (slot 1 boots) — only provisioning/refetch.
Going KMS-only requires break-glass tooling for slot-0 blobs first.

## Next session — candidates

Nothing urgent is open. Queued by preference:
1. **SideroLink / control channel v3** (idea `9da98ad2`) — tunnel
   from kernel args, pre-STATE, KMS in-tunnel; would fix the
   KMS-must-be-WAN wart properly.
2. **Sealed-secrets controller upgrade** (debt `4d6d9e26`, +later) —
   vendored v0.27.3 → 0.38.x.
3. Threads to re-read before acting: `9b5b204d` (endpoint/VIP —
   mostly absorbed by the tunnel-endpoint decision; close?),
   `19a4c316` (seal window — teeth pulled by slot-1 decision).
4. The original v1 sketch `0e7b7d36` is fully realized — close it
   next session after a skim of its annotations.

## Gotchas that cost time (today's additions)

- **bech32 from memory: one bit wrong in generator[1]**
  (0x26508e6b vs 6d). The cross-check test against the real age
  parser caught it — keep that pattern: never trust a hand-rolled
  encoding without an oracle round-trip.
- **Hub netstack MTU 1280**: every WG client must clamp ≤1240 or TLS
  blackholes mid-handshake while pings pass. wgquick output emits
  `MTU = 1240` now.
- **kube-proxy nftables mode**: NodePorts bind primary node IP only
  by default; fixed via `proxy.extraArgs.nodeport-addresses` +
  live DS patch (Talos renders bootstrap manifests once — config
  changes don't reconcile them on a live cluster).
- **Bitnami chart purge**: `bitnami-labs.github.io/sealed-secrets`
  is 404. Any helm-repo Application is a rebuild time bomb — vendor
  release manifests instead.
- **nix vendorHash**: new module (filippo.io/age) = fake-hash dance;
  also a STALE vendor dir under an unchanged hash gives
  "inconsistent vendoring", not a hash mismatch.
- `git add -A` swept in deliberately-untracked files once —
  `.claude/` and `k8s/MIGRATION.md` are gitignored now.
- `fly logs` replays old buffered history — filter by timestamp.

## Loose ends

- [ ] Laptop WG config lives in `/tmp` — move + chmod 600 (user)
- [ ] `argocd-dex-server` pod in Error — OIDC component, unused?
      candidate for disabling in the ArgoCD install
- [ ] `tailscale-operator` Application Degraded — not investigated
- [ ] KMS slot 0 never used at boot (DNS race) — accepted, but if it
      bothers you: KMS endpoint by IP instead of hostname would
      dodge DNS entirely
- [ ] Old kubeconfigs pointing at 10.0.0.x are dead — regenerate

## Taskwarrior map

`task +repo_5efa11ff status:pending list` — 7 pending: registry,
this handover pointer, v1 sketch (close candidate), threads
`9b5b204d` + `19a4c316`, idea `9da98ad2` (SideroLink v3), debt
`4d6d9e26` (controller upgrade). Closed today: sketch c8fa867d
(control channel v2), reprovision day, admin-over-tunnel (+ its
thread), wallet age identity (+ its idea), decisions on endpoint
independence and slot-1 posture.
