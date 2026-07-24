# Handover — next session

_Last updated: 2026-07-24 (late night). Read alongside `docs/vision.md` (why) — this file is the what/where/next._

## Where things stand

### The cluster

- Single control-plane node `b0:41:6f:15:3b:8f` at **10.0.0.20**, Talos v1.12.6, k8s v1.32.3. ArgoCD syncing `k8s/apps` from `main`.
- IP confirmed correct for now; DHCP reservation still not formally set. Endpoint-as-DNS/VIP remains an open thread (task `9b5b204d`).
- Admin talosconfig cert expires **2027-07**, no expiry alarm exists.
- ✅ **wg0 is live on the node** (2026-07-24): tunnel address `10.99.0.54/24`, steady 25s keepalives to fly, bidirectional traffic verified (hostNetwork pod → `nc 10.99.0.1 80` → "hello from the tunnel"; fly logs show `wg hello served to 10.99.0.54`). Rollout path: manual device flow + wallet approval on `/verify` → re-fetched composed config from the (already unsealed) fly server → `talosctl apply-config` (no reboot). **The master signature was never touched** — server-side injection did all derivation. Diff vs WG-off baseline was exactly the wg0 block.

### The config server (`config-server/`, Go) — all deployed & production-validated

| Component | File | Notes |
|---|---|---|
| Config composition | `main.go` | machinery `configpatcher` v1.12.6 (keep in lockstep with cluster) |
| Device flow (RFC 8628) | `deviceflow.go`, `oauth.go` | in-memory, single-use MAC-bound tokens |
| Wallet approval | `siwe.go` | EIP-191 recovery vs allowlist |
| **SIWE status page** | `status.go`, `session.go` | single-use login nonce → 12h HttpOnly cookie; zero-leak logged out |
| **WG key derivation** | `wgderive/` | HKDF from one master; **frozen v1 contract**, stability tests pin vectors — breaking them re-keys the fleet |
| **WG injection** | `wgkeys.go` | `wg0` patch composed at serve time only; byte-identical output when WG off (verified vs pre-slice binary) |
| **Sealed unseal** | `wgseal.go` | wallet sig over `talos-config/wg/master/v1` → master in memory only |
| WG userspace device | `wg.go`, `wgbind.go` | custom `fly-global-services` bind (mandatory on fly) |
| Test client | `cmd/wgping` | `-master`/`-sig` modes impersonate a machine end-to-end |

### Root of trust: the admin wallet

```
admin wallet 0xf568...9406 (EOA, RFC 6979 deterministic)
  → EIP-191 sig over "talos-config/wg/master/v1"  ← SIGNATURE IS THE MASTER KEY
    → HKDF master (memory only, never at rest)
      → server WG key + per-machine keys (MAC-keyed) + tunnel IPs (10.99.0.0/24, server .1)
```

- **Never sign the master message anywhere** except the server's `/verify` page or offline `cast wallet sign`. Any captured signature = fleet master until message rotates to `/v2`.
- Server restart → **re-seals**. `/sealed` returns 503 until an admin signs at `/verify`. Config serving also 503s while sealed (deliberate: don't strand machines outside the tunnel).
- Pinned server pubkey in `fly.toml` (`WG_SERVER_PUBKEY = uNUI+XlWP/1Q...`) — wrong-wallet unseal fails.
- Decision recorded (query `status:any +decision`): sealed-server accepted; human re-unseal after restart is fine because the tunnel has no unattended consumers yet.

### Fly deployment

- App **marnyg-talos-config**, `arn`, single always-on machine (in-memory state — do NOT scale up).
- Secrets: **only `AGE_KEY`** remains. `WG_PRIVATE_KEY`/`WG_PEERS` retired.
- Dedicated IPv4 **213.188.219.215** (UDP :51820). Deployed 2026-07-24, unsealed by admin wallet, pin validated.
- Sealed-state monitoring: `.github/workflows/sealed-check.yml` probes `GET /sealed` every 15 min.

## Status page — SHIPPED & validated live (2026-07-24 late night)

`/status` (read-only) behind SIWE session login. Login signs
`talos config-server status login\nnonce: <n>` — an ordinary auth
message, distinct prefix from both the approval and master messages; a
captured login signature grants nothing but a 12h session. Logged out
the page is only the sign-in prompt; without `--admin-address` it 404s.

Shows per machine: WG last handshake / rx / tx / observed WAN endpoint
(parsed from the device UAPI, `parsePeerStats`), last config fetch per
MAC, plus the auto-bootstrap loop snapshot (`bootSnapshot` — including
`multi-cp-refused`, which was previously invisible), seal state, and
the deployed `FLY_IMAGE_REF`. Auto-refreshes every 30s.

Also in this batch: `controlPlanes` now parses `machine.type` from the
base config instead of matching "controlplane" in the filename (the
single-CP guard no longer trusts file names), and the HTTP route mux
moved out of the test file into `main.go` so tests exercise the real
routing.

Live validation: deployed `deployment-01KYA6J6MS` (git `417114d`),
unsealed, first status login recorded, node re-handshaked ~50s after
unseal, auto-bootstrap re-observed `etcd-running → idle`. During the
sealed→reconnect window the server logs
`Failed to send handshake initiation: no known endpoint for peer`
every ~5s — known-benign wireguard-go retry noise; stops the moment
the node dials in.

## Auto-bootstrap — SHIPPED & validated live (2026-07-24 night)

`config-server/bootstrap.go`: 30s poll loop, dials apid at the derived
tunnel IP through the netstack, mutual TLS with a short-lived os:admin
cert minted from the OS CA (extracted from the machine's own composed
config — no extra secrets plumbing). Pure decision core `bootState`
(unit-tested): etcd `waiting` ×2 consecutive → one `Bootstrap` per
lifetime; `Running` is terminal. Guards: exactly ONE declared CP
(multi-CP refused — split-brain), sealed = inert. Opt-in
`--auto-bootstrap`, set by the fly entrypoint.

Live validation: deployed, unsealed, observed `etcd-running → going
idle` against the real node. The *action* path (actual Bootstrap call)
is unit-tested but fires first on the next freshly provisioned CP —
watch fly logs during the next PXE boot.

**Gotcha found live: apid cert SANs don't include wg0 addresses** (node
address discovery skips it) — mutual TLS failed as a bare
"unreachable". Fixed twice: live node patched manually
(`certSANs: [10.99.0.54]`), and injection now appends the tunnel IP to
certSANs for future machines (append semantics test-covered). RPC
errors now logged on change (not swallowed).

`40ea519` (certSAN injection + RPC logging) went live with the
`01KYA6J6MS` deploy — nothing is waiting on a deploy anymore.

## Next session (sketch `c8fa867d`)

1. **KMS disk unseal** — the last major slice. Per-boot disk keys via
   the siderolabs KMS protocol, passphrase break-glass slot mandatory
   (recorded decision — TPM would void revocation). Open design
   question to settle FIRST: whether STATE-partition unlock can reach
   a KMS over wg0 at all (wg0 comes from machine config, which lives
   on STATE — possible chicken-and-egg; Omni avoids it because
   SideroLink is kernel-arg-configured). May force WAN-exposed KMS
   gRPC with UUID-keyed derived seals instead of tunnel-only.
2. Parked nearby: task 29 (derive age identity from the unseal
   signature — retires AGE_KEY, the last fly secret), task 28 (admin
   access over tunnel), thread 9b5b204d (endpoint DNS/VIP).

Gotchas learned this session: `talosctl pcap --bpf-filter` takes
`tcpdump -ddd`-style instruction lists but silently didn't filter
server-side — and an unfiltered pcap captures its own gRPC stream
(~0.5 GB/30s feedback loop); filter offline. Default namespace enforces
baseline PSS (no hostNetwork pods) — use kube-system. Repo kubeconfig
context points at a dead OIDC setup; `talosctl kubeconfig` works. nix
vendorHash depends on the **import graph**, not just go.mod/go.sum —
stage new .go files before computing. Flake builds ignore untracked
files.

## Gotchas that cost time

- **`fly-global-services` DNS lookup hangs ~20s off-fly** (no timeout in `wgbind.go`) — local WG-enabled starts are slow; filed as broken window.
- **Redeploy = restart = resealed.** Every `fly deploy` needs a follow-up unseal signature.
- **nix vendorHash**: `go.mod` changes need fake-hash dance. This slice avoided it (stdlib `crypto/hkdf`, Go ≥1.24).
- configpatcher returns input **verbatim with zero patches** but re-renders (adds `token: ""` etc.) with ≥1 — byte-diff comparisons must use the same render path.
- Earlier gotchas (poll interval ≥6s, fly UDP dedicated v4, `nix run` silent rebuilds) still apply.

## Loose ends

- [x] ~~Uptime pinger on `/sealed`~~ — GitHub Actions `sealed-check` every 15 min (fails → email). Deliberately NOT a fly health check: fly restarting a sealed machine = unrecoverable seal loop.
- [x] ~~`40ea519` awaiting deploy~~ — live in `01KYA6J6MS`
- [x] ~~`wgbind.go` DNS timeout~~ — was already fixed (2s bounded lookup); stale task 27 closed
- [ ] DHCP reservation for 10.0.0.20 — user deferred, IP confirmed correct for now
- [ ] `k8s/MIGRATION.md` untracked — user said leave it
- [ ] `.claude/` untracked in repo root — gitignore?
- [ ] User's default kubeconfig context is stale (dead zitadel OIDC) — repoint or prune
- [ ] Handshake-initiation log spam while a peer is away — known-benign, not filed

## Taskwarrior map

`task +repo_5efa11ff status:pending list` — sketch `c8fa867d` carries the slice-by-slice history as annotations. Decisions closed: build-not-Omni; no OIDC/chain RPC; fly per-boot dependency accepted; wallet-rooted sealed-server unseal. New since last update: task 28 (+thread, admin access over tunnel), task 29 (+idea, wallet-derived age key), stale task 27 closed.
