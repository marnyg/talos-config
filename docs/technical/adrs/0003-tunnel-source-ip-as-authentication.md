# ADR-0003: Tunnel source IP as authentication for admin routes

- Status: Accepted _(records a decision implemented and deployed 2026-07-29)_
- Date: 2026-07-29

## Context and Problem Statement

`nix run .#apply` must fetch the hub-composed machine config (the hub
injects wg0 keys, certSANs, and disk-encryption state at serve time; a
locally composed config strips that state from a running machine — the
cp1 incident). The hub therefore serves `GET /config` on its tunnel
listener (`10.99.0.1:80`). How is that route authenticated without
adding state or per-request wallet friction?

## Decision Drivers

- Invariant 1: no identity/membership state outside git + owner keys.
- Invariant 2: nothing a server remembers may be non-derivable.
- `apply` is a routine operation — a wallet signature per fetch is
  unacceptable friction for a device already enrolled as an admin peer.
- The public HTTPS listener must never expose composed configs.

## Considered Options

### Option A: SIWE signature per request

Wallet signs a challenge for every config fetch.

- Pros: strongest per-request proof; mechanism already exists.
- Cons: wallet interaction on every `apply`; the wallet's job ended at
  enrollment — re-proving identity per request adds nothing the tunnel
  key doesn't already prove.

### Option B: Bearer tokens / mTLS client certs

Hub-issued credentials for admin devices.

- Pros: conventional.
- Cons: issuance + expiry machinery; either state (violates invariant
  2) or yet another derivation tree duplicating what wg enrollment
  already provides.

### Option C: Tunnel source IP under cryptokey routing (chosen)

WireGuard's cryptokey routing guarantees a packet with source IP X can
only arrive through the peer session whose key is bound to X. Admin
peer keys/IPs are HKDF-derived from the wallet master and assigned at
enrollment (`wgup`, wallet-signed). The tunnel listener checks
`r.RemoteAddr` against the derived admin IP set (`requireAdminPeer`,
`wg.go`) — possession of the tunnel session *is* the credential.

- Pros: zero new state; zero new round trips; auth reduces to the
  existing wallet-rooted derivation chain; unreachable from the public
  listener by construction.
- Cons: device-level, not user-level — anyone holding the enrolled
  device's tunnel key is an admin; auth strength is exactly tunnel-key
  custody.

## Decision Outcome

Chosen: **Option C**. The wg session key is already a wallet-authorized,
git-derivable admin credential; re-authenticating on top of it would
duplicate trust already established at enrollment.

### Consequences

- The admin surface is only as strong as device key custody (accepted:
  same posture as `talosconfig`/`kubeconfig` held on the same devices).
- The check is only sound on the tunnel listener — the route must never
  be mounted on the public mux, and forwarded-header values must never
  be consulted (`RemoteAddr` only).
- **Mesh v2 must carry the property over**: nebula's lighthouse/firewall
  enforces the cert↔overlay-IP binding, so "source IP = authenticated
  membership" holds there too; the phase-1 tunnel `/config` route on the
  nebula listener must keep the derived-admin-IP gate.

### Confirmation

Deployed 2026-07-29 and verified against the live hub (non-admin peers
refused, `apply` fetches succeed only from enrolled admin devices).
Invalidated if the mesh ever admits peers whose overlay IP is not
bound to a wallet-derived identity.
