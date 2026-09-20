# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-20 (eighth session) — **P2.2 is live; `359.9.2` closed.**

- `p0agent` 0.1.3 (the `z2go` agent, `d12d1e6`) on **w1** (00:20Z)
  and **cp1** (00:27Z) via `talosctl upgrade`; installer
  `v1.12.6-p0agent-0.1.3@sha256:923158ad…` pinned in both hardware
  files (`527d099`). `get extensions` finally reports the true version.
- **Bug found by the deploy, fixed in `98acac7`:** cp1 admitted the
  hub's `apid` stream fine, but the hub logged `name resolver error:
  produced zero addresses` → `unreachable`. Machinery hands a single
  endpoint to gRPC as `dns:///cp1.mesh.internal`, so gRPC's DNS
  resolver ran *on the hub* before the facet dialer could. Fix:
  `facetResolver` (per-ClientConn via `grpc.WithResolvers`) shadows
  the `dns` scheme and passes the name through; `bootstrap_client_test.go`
  drives the client over a `net.Pipe` facet with an unresolvable name
  and fails without the fix.
- **Live acceptance passed** (hub `98acac7`, unsealed 00:33:54Z,
  hubkey `4034b889…`): both nodes logged `connection to hub … lost;
  beating` → `beat ok` within 8 s; hub `node-unknown` at 00:34:00 →
  **`etcd-running` at 00:34:36** — 42 s unseal-to-known, under the
  ~1 min bar. (No `hub sealed` retry was exercised: the unseal
  preceded the nodes' 32 s loss detection.)
- Broken windows (`510bf18`): `bootstrapper.dial` seam +
  `TestObserveOverFacet` (observe → talosClient end to end);
  `talos/extensions/installer.env` is now the one place the Talos
  version + official extension refs live (`build.sh` sources it and
  prints the `tag@digest` to pin); relay-child "did not reach
  established state" baselined at ~1.7/min loopback noise (notes.md).

## Loose threads

- **Mac `irohup` switch not yet done** — needs sudo. The closure is
  built (`~/git/nixos`, flake.lock bumped to `2155da7`, uncommitted):
  `cd ~/git/nixos && sudo darwin-rebuild switch --flake .#mac`, then
  commit the lock. The running daemon (`kdhgj9…`) is the pre-`z2go`
  binary; `ipt7`'s fix is already in it, so nothing is broken meanwhile.
- The sealed-hub flat retry (`ErrHubSealed` → `MinRebeat`) is covered
  by the relay e2e but has not been seen live; a slow unseal on the
  next redeploy will show it.
- `/status` needs a wallet login; the hub's `auto-bootstrap:` log lines
  (`fly logs`) carry the same observation and were what acceptance
  read.
- Domain-model entry "The hub as a caller" says *built 2026-09-21*;
  it is live as of 2026-09-20 (dates in the 09-21 entries look off by
  a day) — cosmetic.
- `0q0` blocked on capacity; ADR-0011 vs invariant 2 ruling still open.
- Route-churn restart path unobserved (`7c3`); control socket (`fgr`);
  mobile `fakeip` (`phz`); cp1 hostname pin (`t7b2`).

## Suggested next steps

- Run the Mac switch (above); commit `~/git/nixos` flake.lock.
- **P2.3** (`359.9.3`): the in-cluster gateway pod — terminates
  identity streams, forwards to Services, injects the verified
  device-identity header (revises ADR-0007); Jackett first.
