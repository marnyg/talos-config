# Handoff — sovereign-actor protocol

<!-- "Where we left off" for the protocol scope. Overwritten per session.
     Deployment (talos/hub/fly) context lives in the root handoff. -->

## Last session

2026-09-16 — **Protocol ADR-0003 drafted (Proposed) from the talos hub
actor-cut design; no protocol code changed.**

- **ADR-0003**: a receiver answers for a principal it holds a live
  `speak-as` from — `VerifyChain` rule 4 becomes `eff.Target ∋ R` **or**
  `∋ P` for a receiver-held `speak-as` P→R, never one taken from the
  caller's bundle. Motivation: a rotating hot key (root ADR-0018) kills
  every grant that names it; grants should name the root. Task `kau`,
  model first (`authorize.qnt`: one law + the caller-bundle mutant),
  then Go. Dialing/addressing the root was ruled out — `ed:` id is the
  iroh `EndpointId`, the TLS pin and Reply check hang on it.
- Root ADR-0024 is the consumer: the talos hub's `#renew`/`#bundle`
  facets are the wallet's, served by whichever process holds the
  unseal. Lighthouse `#publish`/`#lookup`/`#frontdoor` stay M3
  (`0bc.3`); in the talos deployment Phase 1 the lighthouse is a view
  over the Issuer's location cache filled by piggybacked `loc`.
- Mesh v3 Phase 0 gate passed 2026-09-16 (root `b2t`): M3 is unblocked
  by the gate, still blocked by `xwu`.

## Loose threads

- Pre-work order for the talos consumer: `xwu` → `kau` → `7ei` (all
  small, all `authorize.qnt`/actor runtime). `7w5`, `s8n`, `p5g` stay
  independent.
- ADR-0002 and ADR-0003 both Proposed → Accepted when `xwu` / `kau`
  land.
- Glossary *Authorize* still says "require target ∋ R"; amend with the
  rule-4 extension when `kau` lands (proposed, not yet written).
- Two unchosen numbers, no bead: `DefaultMailbox = 64`, renewal-beat
  fraction.

## Suggested next steps

- `xwu` then `kau`: edit `authorize.qnt` first, add the law + mutant to
  `TestFaultPairSweep`, then Go 1:1 in `cert/authorize.go`
  (`chainUnder` rule 4 takes the receiver's aud-side `SpeakAs`).
- `7ei` in the actor runtime (`renew.go`: resolve `old.Aud` via
  `speaksFor`).
