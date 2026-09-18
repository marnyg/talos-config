# Handoff — sovereign-actor protocol

<!-- "Where we left off" for the protocol scope. Overwritten per session.
     Deployment (talos/hub/fly) context lives in the root handoff. -->

## Last session

2026-09-18 (second session) — **ADR-0004 Accepted and built** (`zeb`,
commit `c4f5389`): `cav.target` admits the wildcard `"*"`.

- Model first: `authorize.qnt` `TARGETS ∪ {ANY}`, `intersectTarget`
  identity case, `answersFor` ignores it, law
  `invWildcardTargetNeverWidens`, witness `wildcardTargetTest`;
  `check.sh run` green (authorize exhaustive depth 2 ≈ 173 s under
  `verify`).
- Go 1:1: `TargetAny`/`IsTargetAny` in `cert.go`, `DecodeCert` rejects
  a mixed `["*", ed:…]` set, `intersectTarget` in `authorize.go`, the
  root filter in `verifyChain` rejects a consent whose target is `{*}`
  (decision `znk`: wildcard is grant-only; `invConsentTargetsSelf`
  unchanged), rapid law + chain-law cases.
- First consumer of the wildcard is live: the talos policy compiler
  (`config-server/policy`) emits every grant with `target: ["*"]` and
  its round-trip suite exercises `cert.Authorize` with them.

## Loose threads

- Decided in drafting, confirmed by landing, never discussed: mixed
  target sets reject at decode (not "ignore the `*`"); a `*` consent
  roots nothing rather than failing rule 4 later. Veto ⇒ reopen ADR-0004.
- `actor.Hold` has no ADR — `t29` (draft ADR-0005 if the pattern
  spreads).
- The held `speak-as` stays out of `Result.Verified` (ADR-0003); no
  facet→verb table yet; `DefaultMailbox = 64` and the renewal-beat
  fraction remain unbeaded.

## Suggested next steps

- No protocol change is queued by the talos consumer: `Issuer#bundle`
  (`359.8.2.3`) and the cp1 agent (`359.8.3`) use `cert` as is.
- M3 `0bc.3` (lighthouse actor, `#publish`/`#lookup`, PoW postage for
  strangers) is the next protocol-side build when picked up.
