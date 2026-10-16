# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-15: **pins wired into authorization (`d0o0`, protocol ADR-0011
Proposed)** — the follow-through on ADR-0010 that makes a rotation
*mean* something at a receiver. Swarm of three (`c5705eb` ADR,
`0870a68` `authorize.qnt`, `3388cac` code), gates re-run
(`quint test/run/verify`, `go test` protocol + config-server,
`scripts/test-iroh.sh`). FINDING on the way: the hop must be
subtractive for pinned addresses (old EOA key == identity address).
Details in `protocol/docs/day-to-day/handoff.md`. Earlier the same
day: `aja2` closed (ADR-0010 + `rotation.qnt` + `cert/rotation.go`,
`87a3ac1`), desired-state amended per owner ruling.

- **Deployed 2026-10-16 11:19Z** (`fly/deploy.sh` via the nixos
  builder, image `341b542`, machine v83), unsealed 11:21:17Z, hubkey
  `2a812ca5…`. Every pin is trivial (`{root}, k=1`) so no verdict
  moved; `marius-mac` renewed 11 s after unseal, cp1 answered at
  +13 s, zero hub warnings. Relay loopback-probe noise ~2/min vs the
  1.7/min baseline — within noise.

## Loose threads

- `d0o0.1/.2/.3` merged, **awaiting owner close**; `d0o0` stays open
  for the durable `talos/` copy of pins/rotations (not safe-to-lose
  per ADR-0010) — or re-scope and close.
- ADR-0011 Open: rule 3 (aud binding) not pin-aware; `authorize.qnt`
  pre-d0o0 laws quantify the unpinned receiver (bridge laws cover).
- `unsealIssuer` logs/pairs the admitting KEY, not the identity —
  revisit against ce8 when a real rotation exists.
- vendorHash for config-server-bin / iroh-transport / actors-bin
  re-bumped this session (protocol/ changed again).
- Standing: `e6pu.2.{1,2,3}` awaiting close since 10-12; `whxz`,
  `e7ru`, `4mg`; 9 `<!-- stale? -->` flags; `jlgz`, `bsj`, `bhui`.

## Suggested next steps

- Close the three `d0o0` tasks; decide `d0o0`'s remaining scope
  (`talos/pins/<address>.json` → hub `Pin.Hold` at construction).
- Back to `ivof`: sandbox-agent live acceptance, then the parent actor.
- `4mg`: EIP-712 + seed decoupling as one decision.
