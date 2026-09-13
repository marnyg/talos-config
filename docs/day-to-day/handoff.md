# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-13 (second session) — **protocol threads ruled, Phase 0 chosen
over M3, P0.1 passed** (`main bdf5488 → 05f8684`, spike branch
`spike/mesh-v3-p0` merged `--no-ff`).

- Rulings, all as recommended: `0lo` absent `endpoints` = ∅ (decision
  `eak`), `5yj` strict per-edge `Send` for v0 (`zey`), `eig` no cache
  refresh on reject (`5qt`), `7n8` chain cap is a deployment number
  (`seb`, bead deferred). `xwu` (verb = root consent's verb, now blocks
  `0bc.3`), `7w5` (silent close on bad-sig), `7ei` (`#renew` via
  `speaksFor`), `s8n` (mark learns from aud-side speak-as) became tasks
  with acceptance. Protocol ADR-0002 records the two semantic ones.
- Milestone pick: **Phase 0 before M3**, order `359.1.1 → .1.3 → .1.2`.
- `359.1.1` **PASS** (closed): scratch fly app `marnyg-iroh-relay-spike`
  runs the upstream `iroh-relay:v1.1.0` in plain-HTTP mode behind fly's
  TLS proxy (`fly/relay-spike/fly.toml`); different-NAT peers connect
  via relay, LAN-direct punch < 1 s without QAD, pcap shows no n0
  hosts. Full data in `docs/mesh-v3-iroh.md §P0.1`; ADR-0022 drafted.
  Tooling: `iroh-go/cmd/p0relay` (two-peer probe), `.#p0relay-static`
  (musl binary for containers/nodes), `P0_LOG=debug` for iroh tracing.
- Merged-but-open beads from the M2 swarm closed (`kp4 6tf 02j djs ax7`).

## Loose threads

- **Scratch relay app is up and open** (`access = everyone`), a second
  public surface accepted as spike scope until `kql` tears it down
  after the gate `359.1.5`. The `p0peer` registry tag goes with it.
- `nixos` (`mar@nixos`, 10.0.0.11) has a temporary `iptables -I
  nixos-fw -i wlp12s0 -p udp -j ACCEPT` (until reboot) and a clone at
  `~/p0`; `~/p0/result` is the last `p0relay-static` build.
- The owner laptop's Cisco socket filter blocks LAN UDP from unsigned
  binaries (`dj5`); it is a relay-only peer for any probe.
- Follow-ups filed: `p5g` (no-QAD relay option in `iroh-transport`),
  `5gz` (Phase 1 relay embedding in config-server), `0pq` (QAD
  trade-off), `4un` / `359.8.5` / `6z9` carried; ADR-0017/0019/0022
  and protocol ADR-0002 are Proposed.

## Suggested next steps

- `359.1.3` Talos extension proof: static agent (`.#p0relay-static`
  pattern) as a system extension via the image factory, dialing the
  scratch relay; needs the cluster reachable (start `nebup`;
  `talosctl` hung without it) and the ADR-0019 NTP-gate question.
- Then `359.1.2` (Android feasibility), then the gate `359.1.5`.
- `xwu` before M3 — model first (`authorize.qnt`), then Go.
