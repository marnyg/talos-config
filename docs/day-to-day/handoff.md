# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-18 — **Protocol pre-work steps 2 and 3 of 3 landed: `kau`
(VerifyChain rule 4, protocol ADR-0003 → Accepted) and `7ei` (`#renew`
aud binding). `359.8.1` is no longer blocked on the protocol side.**
Protocol code only; no talos/hub/k8s change.

- `kau`: a receiver answers for a principal P whose live `speak-as P→R`
  it *holds* — `eff.Target ∋ R or ∋ P`. Go groups the receiver-held
  inputs as `cert.Receiver{ID, Consents, SpeakAs}` so held vs.
  caller-bundle speak-as is a type. Consequence for talos: the hub's
  consents name `{hubkey, wallet}`, grants to hub facets name
  `target: wallet`, the hub passes its unseal `speak-as` as
  `Receiver.SpeakAs`; grants survive `hubkey` rotation.
- `7ei`: `#renew` accepts a held cert whose `aud` is the caller's cold
  principal when the proof carries a live `speak-as` to the calling hot
  key (`cert.SpeaksFor`, rule 3's predicate).
- Three debt items fixed in passing (ADR index for 0002, `Bundle` doc,
  monotonicity laws over the held speak-as). Details in
  `protocol/docs/day-to-day/handoff.md`. Commits `a2b0703`, `df7e1d5`,
  `e469679`, `1f8865a` (+ this docs-update).

## Loose threads

- Root ADR-0024 (hub actors cut by key) stays Proposed until `359.8.2`
  builds it; its rule-4 prerequisite is now Accepted.
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

- `359.8.1` membership issuance against ADR-0018 + ADR-0024 — the hub
  as the protocol's first real consumer (hubkey random per process,
  `/unseal` signs the `speak-as`, member cert mint/verify/renew).
- `359.8.2.2` (embed the relay in the hub, `5gz` shape) is independent
  and ready if you'd rather ship something.
