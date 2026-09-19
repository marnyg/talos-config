# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-20 (sixth session) — **`ipt7` fixed and verified live**: a hub
redeploy no longer strands a running `irohup -tun` daemon. Commit
`5c6e506`; hub image `registry.fly.io/marnyg-talos-config:5c6e506`,
unsealed (hubkey `a2fdf950…`); Mac daemon `kdhgj9…` (nixos flake input
`talos-config` bumped to `5c6e506`).

- **Beat on staleness evidence** (`config-server/nodeagent/{agent,
  caller}.go`): `Agent.Dial` treats `ErrUnreachable` / `ErrUnknownName`
  as "the directory is a beat old", re-beats (serialized, rate-limited
  to `MinRebeat` = 1 min) and retries once on the fresh record; a
  refusal is returned as-is. Each candidate dial is bounded by
  `DialTimeout` = 15 s instead of hanging for iroh's idle timeout. The
  tun resolver `Kick()`s the loop on an unknown in-zone name, so a
  member that enrolled since the last beat resolves on the next query.
  The beat loop is a timer + kick channel; `Beat()` and rebeats share
  one mutex.
- **Live acceptance** (01:07, daemon *not* restarted): first
  `hub.mesh.internal` flow after the redeploy = 15 s dial timeout on
  the dead key → `renewed … at ed:a2fdf950…` → `beat ok` → served;
  every later flow ~30 ms; cp1/apid unaffected. e2e test
  (`nodeagent_iroh_test.go`) now ends with a hub redeploy under a new
  hubkey behind the same URL.
- **The 09-19 "general network loss" is explained and is not the
  daemon**: system log shows router DNS on `en7` dead 21:40:02–21:40:58
  (413 queries, ~20 answers), self-healed a minute *before* the daemon
  restart at 21:42:06; no route/resolver/interface change in the
  window. This session's deploy: zero ping loss, WAN DNS and tun DNS
  answering on every 2 s probe. Two overlapping events looked like one.

## Loose threads

- **First flow after a redeploy still waits on the pooled dead
  connection** (`cmd/irohup/pool.go`): `Conn.Open` on a QUIC connection
  whose peer vanished only fails at the idle timeout; the pool learns
  it is dead then. Later flows recover in ≤ 15 s + one beat. Bound
  `Open` if it bites.
- **`await` leaks a late success** (`iroh-transport/stream.go:130`): a
  dial that completes after its ctx timed out is never `Destroy`ed.
  Harmless at one dial per 15 s; a broken window, not a bug in play.
- **Nodes (cp1/w1) still learn the new hubkey only at their beat** —
  harmless today (nothing dials the hub between beats on a node), but
  P2.2 puts the hub on the dialing side and the hub's location table
  is empty after a deploy until members beat (≤ 6 h). Same shape as
  `ipt7`, other direction; the `Kick`/rebeat primitives are there.
- `0q0` blocked on capacity; ADR-0011 vs invariant 2 ruling still open
  (see previous handoff's note — unchanged).
- Route-churn restart path unobserved (`7c3`); control socket (`fgr`);
  mobile `fakeip` (`phz`); cp1 hostname pin (`t7b2`).

## Suggested next steps

- **P2.2 (`359.9.2`)**: hub→node dials onto identity streams; decide
  first how the hub re-learns node locations after its own restart
  (nodes re-beat on a hub-side signal? short first beat after deploy?).
- Close `ipt7` after living with one more routine redeploy.
- Fire `7c3` once deliberately.
