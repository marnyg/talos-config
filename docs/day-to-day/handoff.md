# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-19 — **`#bundle` carries the name map; `359.8.2.3` closed.**
Commits `f725938`, `859e4a7`, `5bec12b`. ADR-0024's outstanding list
is down to Provisioner-as-actor.

- **Ruling (decision `2fc`)**: the name map is *not* a pure function
  of git — that wording predates actor sovereignty. Git holds names,
  members mint keys (ADR-0015), and invariant 1 forbids the hub a
  registry. So name→NodeId is **witnessed**: each `#bundle` caller's
  verified member cert is the proof of its own binding and ships
  as-is (no second signed format). NodeId→endpoints is the member's
  own piggybacked `reach-me-at`.
- **`config-server/issuer/namemap.go`**: `Bundle.NameMap
  []NameEntry{Member, Location?}`, wire `name_map`. `Issuer.members`
  is a safe-to-lose witness cache: newest `iat` wins, expired evicted,
  blocklisted filtered on the way out (unblock resolves again without
  a re-beat). `Lookup(entries, name)` may return two NodeIds across a
  re-key. `DecodeBundle` checks shape (verb `member` + sig; location =
  verified `reach-me-at` issued by that member).
- **`protocol/actor.Locations(ids...)`**: batch read of the location
  cache under one lock/one clock; the name map joins through it.
  `location.go` + protocol glossary: `exp ≈ 1 h` is ADR-0001's sketch
  for a roaming actor, not a rule.
- `TestHubBeatOverIroh` now publishes the member's location and
  asserts the map. Both `vendorHash`es bumped (`config-server`,
  `iroh-transport`); `nix build` green for both.
- Docs: ADR-0024 amendment 2026-09-19, glossary "Name map", Issuer
  row, ADR-0017 name-map line, `mesh-v3-iroh.md` §hub + invariants
  table row 2.

## Loose threads

- **Members must persist their last name map** — the hub's cache is
  empty after every deploy until others beat (noted on `359.8.3` and
  `359.8.4`). Whether the Kit should carry a hub location is still
  theirs to decide.
- **ADR-0024 stays Proposed** until Provisioner-as-actor lands or is
  ruled a follow-up ADR. `359.8.2.3`'s "hub-http shrunk to `/config`"
  was spec-only (no stream facet exists in code) — closed with that.
- **Cold cache is two GETs** (`speak-as` + `reach-me-at`); ADR-0024
  reads "one `/.well-known` fetch" as one hostname — veto if you want
  one document.
- `Dockerfile.siweoidc` is broken since the `../protocol` replace;
  surfaced, not fixed. No graceful shutdown in `config-server`.
- Carried: `tqr` (flip `/sealed` on identity once a member beats),
  `kql` (blocked on `359.8.3`), `t29` (`Hold`/`Multi` ADR — a third
  runtime addition makes the pattern), `DefaultMailbox = 64` /
  renewal-beat fraction unbeaded, w1 down (`0q0`, `kso`), `5gz`.

## Suggested next steps

- **`359.8.3`** cp1 agent — the first real member: NodeId on iroh
  homed at the hub's relay, fetch `/.well-known/talos-hub/{speak-as,
  reach-me-at}`, enroll (v2 message → Kit), beat `#renew`+`#bundle`,
  persist Kit + name map, consume `policy.AcceptTable(KindNode)`.
  Build via `talos/extensions/p0agent/build.sh` (musl chain warm on
  `mar@nixos`); then `kql` tears the scratch relay down.
- Deploy the hub (`HUB_BUILDER=mar@nixos fly/deploy.sh`) so the
  production Issuer serves `name_map` before the agent lands.
- `tqr` once one member beats.
