# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-17 — **Protocol pre-work step 1 of 3 landed: `xwu` (chain verb
= root consent's verb, protocol ADR-0002 → Accepted). Protocol code
only; no talos/hub/k8s change.**

- `verification/quint/authorize.qnt` first, then `protocol/cert` 1:1:
  `cert.VerifyChain(receiver, verb, …)` roots only in consents carrying
  the verb the receiver expects; every link and every speak-as must
  cover it. `Authorize` (the per-connect check) is the `invoke`
  instance; `actor.invokeChain` binds `invoke` for envelopes. Model
  gained a `Publish` verb + two laws + a witness; mutants die. Details
  in `protocol/docs/day-to-day/handoff.md`.
- Three debt items fixed in passing (model `attenuate` verb check,
  `modelVerbs ∋ publish`, `check.sh` re-timed ~135 s).
- Commits `9bf48ac`, `e158fbc` (+ this docs-update). `xwu` closed.

## Loose threads

- `kau` (VerifyChain rule 4, protocol ADR-0003) is next and is the one
  `359.8.1` still needs on the protocol side besides `7ei`; ADR-0003 →
  Accepted when it lands. Root ADR-0024 stays Proposed until then.
- Carried into implementation, not blocking: the exact `#mint-device`
  payload (which ADR-0012 approval message Enroll forwards; Issuer's
  own replay check vs Enroll's single-use nonce).
- Domain-model §2 "Policy: payload, not identity" render diagram is
  superseded twice (ADR-0017, `mdv`); redraw when `359.8.2.3` lands.
- Two unchosen protocol numbers with no bead: `DefaultMailbox = 64`,
  renewal-beat fraction.
- Unchanged: cp1 dials the scratch relay until `359.8.2.2` (`kql`);
  `talos/talosconfig` endpoints stale; NixOS box 1TB disk full; w1
  down (`0q0`, `kso`).

## Suggested next steps

- `kau` → `7ei`, both model-first in `authorize.qnt` / the actor
  runtime; run `TestFaultPairSweep` + `TestChainFaultPairSweep` after
  each.
- `359.8.2.2` (embed the relay in the hub, `5gz` shape) is independent
  and ready if you'd rather ship something.
- Then `359.8.1` membership issuance against ADR-0018 + ADR-0024.
