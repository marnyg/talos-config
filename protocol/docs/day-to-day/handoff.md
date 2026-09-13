# Handoff — sovereign-actor protocol

<!-- "Where we left off" for the protocol scope. Overwritten per session.
     Deployment (talos/hub/fly) context lives in the root handoff. -->

## Last session

2026-09-13 (second session) — **the M2 threads are ruled; no protocol
code changed.** Owner accepted every recommendation:

- Decisions (closed at creation): `eak` absent `endpoints` = ∅ (uniform
  with every set caveat; `Decode` already normalises nil → `[]`);
  `zey` `actor.Send` stays strictly serial per edge in v0; `5qt` a
  rejected-but-valid `loc` never refreshes the location cache
  (sovereignty at admission); `seb` chain-length cap is a deployment
  parameter (`7n8` deferred until a test forces the number).
- Tasks with acceptance: `xwu` verb = root consent's verb in
  `VerifyChain` (model first; **blocks `0bc.3`**); `7w5` close silently
  on bad-sig, signed rejects only after the envelope's own signature
  verifies; `7ei` `#renew` resolves `old.Aud` to `inv.From` via
  `speaksFor`; `s8n` (P3) the clock mark learns from the aud-side
  speak-as when `rootCerts` is next touched.
- **Protocol ADR-0002** (Proposed) records the two semantic rulings
  (`xwu` + `0lo`) and why mixed-verb chains and "absent =
  unconstrained" were ruled out.
- Milestone: owner chose **Mesh v3 Phase 0 before M3**; P0.1 (relay on
  fly) passed the same day — root handoff has the detail. `kp4 6tf 02j
  djs ax7` closed.

## Loose threads

- `xwu` is the only protocol pre-work M3 needs; `7w5`/`7ei` are small
  and independent of M3.
- The relay used by `iroh-transport` will have **no QUIC endpoint**
  (root ADR-0022): `Options.Relay` should build a `RelayMap` with
  `quic_port: nil` instead of `RelayModeCustomFromUrls` (`p5g`), else
  every actor start waits 3 s on a dead QAD probe.
- Carried: `4un` (policy-compiler round-trip), `359.8.5`, `6z9`;
  ADR-0002 Proposed → owner flips to Accepted with the `xwu` landing.

## Suggested next steps

- `xwu`: edit `authorize.qnt` (`verifyChain` takes the consent's verb;
  `linksTo`/`speaksFor` check `cav.verbs ∋ verb`; one non-`invoke` law),
  then Go 1:1; run `TestFaultPairSweep`.
- `7ei` + `7w5` together — both are actor-runtime, one test each.
- M3 `0bc.3` after the Phase 0 gate (`359.1.5`).
