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

- **Hub deployed** (`registry.fly.io/marnyg-talos-config:d12d1e6`,
  unsealed 00:08Z, hubkey `2878c8f5…`); `/status` shows auto-bootstrap
  `node-unknown` for cp1 — correct until the nodes beat. **Not yet
  deployed:** `p0agent` 0.1.3 on cp1/w1 (static `nodeagent` via the
  nixos builder, `build.sh`, pin in `talos/hardware/minipc.yaml`,
  `talosctl upgrade` ~11 min each) and the Mac's `irohup` (nixos flake
  input bump to `d12d1e6`). The old agents beat on their 6 h timer
  only, so the hub learns them within 6 h; after that `/status` should
  read `etcd-running (cp1, <NodeId>)` — half the live acceptance.
- **Live acceptance once the nodes run 0.1.3:** redeploy the hub,
  unseal, watch `/status` flip to `etcd-running` within ~1 min; the
  node logs `connection to hub … lost; beating` → `hub sealed (retry
  in 1m)` → `beat ok`.
- The vendor FOD trap bit again (`iroh-transport/*.go` changed ⇒
  `config-server` `vendorHash`); recomputed in `d12d1e6`. Any change
  under a `replace`d tree needs the two-command check in
  `config-server/nix/default.nix`.
- Relay child logs `Connection did not reach established state within
  timeout` from loopback peers a few times after the deploy; no
  pre-deploy baseline — watch, don't chase.
- Broken windows closed in `4756627`: `await` releases a late FFI
  result, `publishLocation` waits for `Online()`, bootstrap's zone
  falls back to `fakeip.Zone`, the v2 `host: hub` row is marked dead
  (removal rides Phase 4, noted on `359.11.2` with the certSAN move).
  Filed: `zbgk` (iroh-ffi watchers unusable).
- `0q0` blocked on capacity; ADR-0011 vs invariant 2 ruling still open.
- Route-churn restart path unobserved (`7c3`); control socket (`fgr`);
  mobile `fakeip` (`phz`); cp1 hostname pin (`t7b2`).

## Suggested next steps

- Ship `p0agent` 0.1.3 to both nodes and bump the Mac's `irohup`;
  run the live acceptance; then close `359.9.2` and `ipt7`.
- P2.3 (`359.9.3`): the in-cluster gateway pod.
