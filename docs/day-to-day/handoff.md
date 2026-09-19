# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-21 (seventh session) — **P2.2 built, not deployed.** Two
commits, both green under `-race` with the relay e2e:

- `4814be3` — **the mirror of `ipt7`** (decision `z2go`): after a hub
  redeploy the hub knows nobody until members beat, and a Talos node
  dials nothing between beats. Evidence it *does* see: (A) the pooled
  QUIC connection its last beat left to the hub closes — iroh
  keep-alives every connection at 5 s, so a dead hub is `Closed()`
  32 s after SIGKILL (measured, relay up or down); `iroh-transport`
  evicts on `Closed()` and reports `Options.OnConnLost`. (B) an
  admitted caller's rooted `speak-as` names a hubkey issued at/after
  ours. Both `Kick()`; `Kick` now *schedules* at the earliest
  `MinRebeat` instead of dropping. A beat refused as `ErrHubSealed`
  retries flat at `MinRebeat` (no cache fallback to the dead key) —
  **every live member has beaten within one `MinRebeat` of the
  unseal.** Beat `Send`s are bounded by `DialTimeout`.
- `49a7bdb` — **the hub as an ordinary caller** (`hubcaller.go`):
  self-minted member cert `{aud: hubkey, name: hub}` + the recipe's
  one host row `{facet: apid, host: hub}` compiled for it, presented
  on the node's `apid` facet via the name map. `bootstrap.go` no
  longer imports `nebstack`; new observation `node-unknown`;
  `mesh-down` → `no-identity-plane`; `--auto-bootstrap` requires
  `--iroh-relay`. `/status` gains a "Members (identity plane)" table
  from `issuer.NameMap()`.

## Loose threads

- **Nothing is deployed.** The hub image needs a `fly deploy`; the
  nodes need `p0agent` 0.1.3 (static `nodeagent` via the nixos
  builder, `build.sh`, pin in `talos/hardware/minipc.yaml`,
  `talosctl upgrade` ~11 min each); the Mac's `irohup` needs the
  nixos flake input bump. Until the nodes are upgraded, a hub deploy
  shows `node-unknown` for up to the old agents' 6 h beat.
- **Live acceptance to run after deploy:** redeploy the hub, unseal,
  watch `/status` → auto-bootstrap `etcd-running (cp1, <NodeId>)`
  within ~1 min; the node logs `connection to hub … lost; beating`
  then `hub sealed (retry in 1m)` then `beat ok`.
- `iroh-ffi` 1.1.0 `WatchHomeRelay` is unusable (sync fn spawning
  outside tokio; drops `is_connected()`) — noted on `z2go`; not
  needed now.
- `hubseal.go publishLocation` publishes before the wan endpoint is
  `Online()` (the e2e had to wait explicitly; fly's loopback relay
  hides it).
- `iroh-transport/stream.go await` still leaks a late success after a
  ctx timeout; exercised more now that beat `Send`s are bounded.
- `talos/mesh-policy.yaml` (v2) still carries the hub→node apid
  firewall row; dead since `49a7bdb`, Phase 4 deletes it with the
  render.
- `0q0` blocked on capacity; ADR-0011 vs invariant 2 ruling still open.
- Route-churn restart path unobserved (`7c3`); control socket (`fgr`);
  mobile `fakeip` (`phz`); cp1 hostname pin (`t7b2`).

## Suggested next steps

- Deploy P2.2 (hub → nodes → Mac, in that order) and run the live
  acceptance above; then close `359.9.2` and `ipt7`.
- P2.3 (`359.9.3`): the in-cluster gateway pod.
