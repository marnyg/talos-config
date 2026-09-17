# ADR-0022: Self-hosted iroh relay behind the hub's TLS terminator, without QUIC address discovery

- Status: Accepted _(drafted 2026-09-13 from Mesh v3 probe P0.1,
  `talos-config-359.1.1`, PASS; accepted 2026-09-17 when Phase 1 task
  `359.8.2.2` embedded the relay in the hub — `config-server/relay.go`,
  commit `240c322` — and `p0relay` confirmed the relay path through
  `https://marnyg-talos-config.fly.dev` at 45 ms, the spike's figure.
  The relay runs while the hub is sealed: it holds no key. Remaining:
  the access hook (`5gz`, now only the membership gate) and the scratch
  app teardown (`kql`, once cp1's agent is repointed in `359.8.3`).)_
- Date: 2026-09-13
- Related: ADR-0006 (remote members relay by default; remote-direct is
  not a goal), ADR-0016 (identity-native mesh), ADR-0021 (in-house
  iroh Go binding), `docs/mesh-v3-iroh.md` §P0.1 (the data),
  `fly/relay-spike/fly.toml`, `iroh-go/cmd/p0relay`, issues `359.1.1`,
  `5gz`, `0pq`, `p5g`, `kql`

## Context and Problem Statement

Mesh v3 needs a rendezvous point every member can reach: the iroh
**home relay**. Invariants 3 and 5 forbid n0's hosted relays (third-
party infra in the connectivity path) and a second public entrypoint,
so the relay must run on the fly hub, under the hub's hostname. The
iroh relay is a Rust binary that wants to *own* TLS on 443 and, for its
QUIC address discovery service (QAD, UDP 7842, the STUN replacement),
must present a certificate on QUIC — which fly's TLS-terminating proxy
cannot front. Phase 0 check P0.1 had to find a hosting shape that keeps
the single entrypoint, needs no certificate plumbing, and still lets
co-located members reach each other directly.

## Decision Drivers

- Invariant 5: one public surface, the hub hostname; no second
  entrypoint, no home-IP pinning.
- Invariant 3 / kill criterion 1: zero n0 infrastructure — no default
  relays, no DNS/pkarr discovery.
- ADR-0006: remote peers (CGNAT, corporate NAT) are relay-by-default;
  **LAN-direct is the win**, remote-direct is not a goal.
- Fly constraints: the proxy terminates TLS for `[http_service]`,
  supports WebSocket upgrades, forwards UDP raw only; one process group
  owns 443 per app.
- Minimal owned code: an upstream relay binary and configuration, not
  a fork.

## Considered Options

### Option A: Plain-HTTP relay behind fly's TLS terminator, QAD off

The relay runs with no `tls` section (`http_bind_addr [::]:3340`,
`enable_quic_addr_discovery = false`); fly terminates TLS on 443 and
forwards the WebSocket upgrade on `/relay` plus the `/ping` and
`/generate_204` probes. Clients use `RelayMode::Custom(https://<hub>)`.
Direct paths come from DISCO exchanging *local interface* candidates
over the relay; peers behind a NAT advertise no reflexive address.

- Pros: no certificate handling anywhere; upstream image runs as-is
  (deployed in ~1 min); 443 stays the one entrypoint; measured PASS —
  different-NAT peers connect via relay, LAN-direct punch < 1 s, packet
  capture shows only the hub host.
- Cons: **no remote hole-punching** (a peer behind any NAT — even
  Docker's — has no reachable candidate; direct paths need one side
  with a LAN-reachable address); every endpoint burns a 3 s QAD probe
  at startup until the client is told the relay has no QUIC (`p5g`).

### Option B: Relay owns TLS; QAD on UDP 7842 passthrough

Give the relay a certificate (LetsEncrypt from inside the relay is
impossible behind fly's 443; a DNS-01 cert would be issued out of band
and shipped as a fly secret) and expose UDP 7842 through fly's raw UDP
service on the dedicated IPv4.

- Pros: full iroh NAT traversal, remote-direct where NATs allow.
- Cons: certificate issuance and rotation become owned machinery in the
  hub (a second cert lifecycle beside web PKI); buys a capability
  ADR-0006 measured as useless for the owner's remote networks
  (symmetric NATs); QUIC on 7842 is a second public protocol surface
  on the same host.

### Option C: Fly process-group sidecar

Run `iroh-relay` as its own fly process group beside config-server.

- Pros: process isolation, independent restarts.
- Cons: a process group is a separate machine and cannot share
  `[http_service]` 443 — the relay would need its own hostname or
  port, i.e. a second entrypoint. Ruled out.

### Option D: Embed the relay in config-server

- Cons: `iroh-ffi` binds only the client `Endpoint`; the relay server
  has no FFI surface. Not available without a fork.

## Decision Outcome

Chosen: **Option A**, because it satisfies invariants 3 and 5 with zero
owned certificate machinery, and its one real cost — no remote
hole-punching — is a capability ADR-0006 already gave up. Phase 1
embeds it as **config-server spawning `iroh-relay` as a child process
and reverse-proxying `/relay` (WebSocket upgrade), `/ping` and
`/generate_204` on its existing listener** (`359.8.2.2`,
`config-server/relay.go`), so the hub's 443 carries the relay protocol
as `docs/mesh-v3-iroh.md` planned. Only those three paths cross the
hub; the relay's index, `/metrics` and the rest stay on loopback. The
binary is the static `/iroh-relay` from the upstream image, pinned to
the same core version as iroh-go. Relay
admission uses the upstream HTTP-POST access hook
(`X-Iroh-Endpoint-Id`), which is how membership will gate the relay.

### Consequences

- Direct paths exist only between members where at least one side is
  reachable on its interface address — the LAN case (nodes, TV,
  laptop on the LAN). Every remote path rides the hub, as today.
- Host firewalls matter: a member behind a NAT can be punched only if
  the LAN-reachable side accepts inbound UDP on the agent's port.
  Talos nodes have no host firewall by default; keep it so for the
  agent port.
- Location records carry `iroh:relay=https://<hub>` as the only
  guaranteed hint; `iroh:udp=` hints are bonuses.
- Follow-ups: `p5g` (tell clients the relay has no QUIC), `5gz` (hub
  embedding), `0pq` (revisit Option B only if remote-direct becomes a
  goal), `kql` (tear down the scratch app after the gate).

### Confirmation

Right if Phase 1's hub-embedded relay shows the same three
measurements from `iroh-go/cmd/p0relay` (relay path from a far NAT,
LAN-direct within seconds when co-located, capture with only the hub
host) and the media kill criterion (80 Mbps sustained, P0.2) holds
over LAN-direct paths. Invalidated if a load-bearing consumer needs
remote-direct (e.g. media over cellular at a bitrate the hub cannot
relay) — then Option B, with its certificate lifecycle, is the path.
