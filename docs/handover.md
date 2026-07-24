# Handover — next session

_Last updated: 2026-07-24 (evening). Read alongside `docs/vision.md` (why) — this file is the what/where/next._

## Where things stand

### The cluster

- Single control-plane node `b0:41:6f:15:3b:8f` at **10.0.0.20**, Talos v1.12.6, k8s v1.32.3. ArgoCD syncing `k8s/apps` from `main`.
- IP confirmed correct for now; DHCP reservation still not formally set. Endpoint-as-DNS/VIP remains an open thread (task `9b5b204d`).
- Admin talosconfig cert expires **2027-07**, no expiry alarm exists.
- ⚠️ The node was provisioned **before** WG injection existed — it has **no wg0 interface yet**. Rolling the tunnel out to it is the first job of the next slice (keys are derivable locally from the master; patch via `talosctl patch mc` or re-fetch).

### The config server (`config-server/`, Go) — all deployed & production-validated

| Component | File | Notes |
|---|---|---|
| Config composition | `main.go` | machinery `configpatcher` v1.12.6 (keep in lockstep with cluster) |
| Device flow (RFC 8628) | `deviceflow.go`, `oauth.go` | in-memory, single-use MAC-bound tokens |
| Wallet approval | `siwe.go` | EIP-191 recovery vs allowlist |
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
- [ ] **TODO: point an uptime pinger at `GET /sealed`** — a surprise fly restart sits sealed (tunnel down) until noticed.

## Next session: tunnel rollout + auto-bootstrap (sketch `c8fa867d`)

1. **Roll wg0 out to the provisioned node** — it never got the injected interface. Either apply the derived patch via `talosctl patch mc` (derive with `wgping -sig <sig> -mac b0-41-6f-15-3b-8f`, mind: don't leak the sig into shell history) or re-serve config. Verify server sees handshake.
2. **Auto-bootstrap slice**: server watches tunnel for approved-but-unbootstrapped CP, calls machinery `Bootstrap` (fly already holds OS CA — no trust escalation).
3. Then: status page behind SIWE session login; KMS unseal over tunnel (passphrase break-glass slot mandatory).

## Gotchas that cost time

- **`fly-global-services` DNS lookup hangs ~20s off-fly** (no timeout in `wgbind.go`) — local WG-enabled starts are slow; filed as broken window.
- **Redeploy = restart = resealed.** Every `fly deploy` needs a follow-up unseal signature.
- **nix vendorHash**: `go.mod` changes need fake-hash dance. This slice avoided it (stdlib `crypto/hkdf`, Go ≥1.24).
- configpatcher returns input **verbatim with zero patches** but re-renders (adds `token: ""` etc.) with ≥1 — byte-diff comparisons must use the same render path.
- Earlier gotchas (poll interval ≥6s, fly UDP dedicated v4, `nix run` silent rebuilds) still apply.

## Loose ends

- [ ] Uptime pinger on `/sealed` (see above)
- [ ] wg0 rollout to node (next slice, step 1)
- [ ] DHCP reservation for 10.0.0.20 — user deferred, IP confirmed correct for now
- [ ] `k8s/MIGRATION.md` untracked — user said leave it
- [ ] `wgbind.go` DNS timeout broken window — not yet filed as +debt
- [ ] `.claude/` untracked in repo root — gitignore?

## Taskwarrior map

`task +repo_5efa11ff status:pending list` — sketch `c8fa867d` carries the slice-by-slice history as annotations. Decisions closed: build-not-Omni; no OIDC/chain RPC; fly per-boot dependency accepted; **wallet-rooted sealed-server unseal** (new).
