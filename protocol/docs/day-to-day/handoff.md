# Handoff — sovereign-actor protocol

<!-- "Where we left off" for the protocol scope. Overwritten per session.
     Deployment (talos/hub/fly) context lives in the root handoff. -->

## Last session

2026-09-18 — **ADR-0004 drafted (Proposed): `cav.target` admits the
wildcard `"*"`.** Design only, from the root `359.8.5` grill-design;
no code. Bead `zeb` carries the build.

- The consumer's compiler cannot enumerate receiver keys (root
  ADR-0015), and ADR-0002 fixed absent = ∅, so kind-wide grants needed
  the wire sentinel ADR-0002 said an "unconstrained" reading would
  require. `"*"` is the identity element of the `target` intersection;
  rule 4 (ADR-0003) is untouched — the receiver's consent supplies the
  concrete `self`. No other set caveat gets a sentinel.
- Ruled out (root exploration-log 2026-09-18): `target: group:<kind>`
  against the receiver's own member cert (kept as upgrade path);
  targets from the location cache; generalising ADR-0003 to "R answers
  for the sovereign it consented to".
- Glossary **Attenuation** gained the wildcard sentence (both scopes).

## Loose threads

- Two ADR-0004 details decided in drafting, not discussed: mixed
  `["*", ed:…]` sets reject at decode; a consent with `target: "*"`
  fails rule 4 by construction. Confirm or veto before `zeb`.
- The 2026-09-17 thread about `actor.Hold` not being an ADR still
  stands (the number 0004 is now taken; use 0005 if the pattern
  spreads).
- The held `speak-as` stays out of `Result.Verified` (ADR-0003); no
  facet→verb table yet; `DefaultMailbox = 64` and the renewal-beat
  fraction remain unbeaded.

## Suggested next steps

- `zeb`, model first: `authorize.qnt` `TARGETS ∪ {ANY}`, `attenuate`
  target case, `answersFor` ignores it, law
  `invWildcardTargetNeverWidens`; then `decode.go` (singleton-set
  rule), `intersectID`, one rapid law. Promote ADR-0004 on landing.
- M3 `0bc.3` stays unblocked on the protocol side.
