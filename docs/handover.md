# Handover — next session

_Last updated: 2026-07-24. Read alongside `docs/vision.md` (why) — this file is the what/where/next._

## Where things stand

### The cluster

- Single control-plane node `b0:41:6f:15:3b:8f` at **10.0.0.20**, Talos v1.12.6, k8s v1.32.3, bootstrapped today via the full wallet-approval flow. ArgoCD syncing `k8s/apps` from `main`.
- Endpoint/certSANs are the raw IP `10.0.0.20` — **DHCP reservation status unconfirmed** (ask!). Endpoint-as-DNS/VIP is an open thread.
- Admin `talosconfig` was regenerated 2026-07-24 (previous cert had silently expired in May). New cert expires **2027-07**; there is no expiry alarm — this will bite again unless the credential story improves (see vision).

### The config server (`config-server/`, Go)

Single binary, all slices production-validated today:

| Component | File | Notes |
|---|---|---|
| Config composition | `main.go` | machinery `configpatcher`, pinned **v1.12.6 = cluster version** (keep in lockstep) |
| Device flow (RFC 8628) | `deviceflow.go`, `oauth.go` | in-memory state, single-use MAC-bound tokens |
| Wallet approval | `siwe.go` | EIP-191 recovery vs `--admin-address` allowlist; admin token = break-glass only |
| WG spike | `wg.go`, `wgbind.go`, `cmd/wgping` | userspace netstack; **custom bind on `fly-global-services` is mandatory on fly** |

### Fly deployment

- App **marnyg-talos-config**, region `arn`, always-on single machine (`min_machines_running=1` — do NOT scale up: flow state is in-memory).
- Dedicated IPv4 **213.188.219.215** (UDP :51820), HTTPS at `marnyg-talos-config.fly.dev`.
- Secrets set: `AGE_KEY` (dedicated deploy key; backup in `talos/fly-age-key.age`, SSH-encrypted), `WG_PRIVATE_KEY` + `WG_PEERS` (**throwaway spike keys — replace in tunnel slice**).
- Wallet allowlist: `0xf56863bF40A75dC3c632C17fc65AA2EB06a89406` (in `fly.toml` env).

### Boot media

- USB ISO: factory schematic `3b388ac6c91e47ac583515063b01c8506becec10643d50c943cf329c739a0b9a` (v1.12.6, kernel args → fly URL + oauth client + extra_variable uuid/mac/serial). Regenerate via `POST https://factory.talos.dev/schematics` if args change.
- Observed fact: real Talos sends `mac`, `uuid`, `serial` as separate form fields to `/device/code`.

## Next session: the tunnel slice (sketch `c8fa867d`)

Goal: every provisioned machine gets a WireGuard interface to fly; server reaches nodes regardless of LAN/NAT.

1. **HKDF per-machine keys**: machine WG privkey = HKDF(master fly-secret, machine uuid). Stateless; pubkeys recomputable server-side. Kill the `WG_PEERS` env.
2. **Config injection**: add `machine.network.interfaces[].wireguard` to composed configs at serve time (server-side patch, not a repo file — key material must not hit git). ⚠️ This touches config composition — byte-diff against a known-good compose and test on the real machine before trusting it.
3. **Peer registration**: server derives all peers from `talos/machines/` at startup (uuid needed — consider recording observed uuid into `meta.yaml` at approval time, or derive from MAC for now).
4. Tunnel subnet plan: server `10.99.0.1/24`, machines assigned deterministically.

Then (separate slices): auto-bootstrap via machinery client over tunnel (fly already holds the OS CA — no trust escalation); status page behind SIWE session login; KMS unseal with passphrase break-glass slot.

## Loose ends / asks for the user

- [ ] DHCP reservation for 10.0.0.20 (MAC `b0-41-6f-15-3b-8f`) — cluster identity rests on it
- [ ] Close task `9770bee2` (v1 build, fully validated) — awaiting confirmation
- [ ] `k8s/MIGRATION.md` untracked — commit or delete
- [ ] Replace throwaway WG fly secrets during tunnel slice

## Gotchas that cost time today

- **nix vendorHash**: any `go.mod` change → set `vendorHash` to fake, `nix build`, copy the `got:` hash. Stale hash gives a confusing vendor-mismatch error.
- **machinery version**: must not exceed nixpkgs Go's supported version; pin to cluster Talos version (currently v1.12.6, `go 1.25.6` directive).
- **Poll interval**: token polls < 5s apart get `slow_down` — scripts sleeping exactly 5s race it; sleep 6+.
- **fly UDP**: wildcard binds receive nothing; bind `fly-global-services` (see `wgbind.go`). UDP needs the dedicated IPv4.
- **`nix run` rebuilds silently** on dirty tree — for quick smoke tests, `nix build -o /tmp/x` and run the binary.

## Taskwarrior map

`task +repo_5efa11ff status:pending list` — key items: sketch `c8fa867d` (control channel v2, spike annotations), thread `endpoint-as-DNS/VIP`, handover task (duplicate of this doc, trimmed).
Decisions (closed, query `status:any +decision`): build-not-Omni; no OIDC/no chain RPC; fly accepted as per-boot dependency.
