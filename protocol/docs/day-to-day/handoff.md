# Handoff — sovereign-actor protocol

<!-- "Where we left off" for the protocol scope. Overwritten per session.
     Deployment (talos/hub/fly) context lives in the root handoff. -->

## Last session

2026-09-17 — **First consumer-driven change: `actor.Hold`.** Commit
`356bd2b` (+ `Authority()` accessor in `03a4769`).

- The talos hub's Issuer (`config-server/issuer`) now `Listen`s for the
  whole process life on the in-memory transport while its authority
  set changes underneath it — empty when sealed, installed at unseal,
  replaced at a re-unseal from the nag window (ADR-0018). The `Actor`
  contract said exported fields are frozen after `Listen`. Rather than
  stop/restart `Listen` around every unseal, **`Hold(consents,
  speakAs)` swaps both under `a.mu`**; `process`, `proofFor` and
  `renew.issuedByMe` read a snapshot (`authority()`), and `Authority()`
  exports a copy for receiver-side `cert.Authorize` runs. Consents and
  SpeakAs stay exported for pre-`Listen` configuration; `Grants` and
  `AcceptTable` are still frozen-after-`Listen`.
- `TestHoldWhileListening` (-race): sealed → unauthorized; `Hold` →
  admitted; 400 flips under concurrent `Send`s; `Hold(nil, nil)` →
  re-sealed.
- Consumer shape worth knowing here: the hub's Enroll actor sends
  `#mint-device` with an **empty chain** — it *is* the consented
  principal (`aud` of the Issuer's sibling consent), so the receiver's
  own consent roots the proof. First real use of "empty is legal".

## Loose threads

- `Hold` is not an ADR. It changes a documented contract of `Actor`
  and picks live-swap over restart — small, but the reasoning lives
  only in the commit and the root ADR-0024 note. Draft ADR-0004 if the
  pattern spreads (e.g. `Grants` needing the same for grant renewal).
- The held `speak-as` is receiver configuration but not R-signed, so
  it stays out of `Result.Verified`/`clock.Mark` (ADR-0003).
- No facet→verb table yet: `actor.invokeChain` is the only verb
  binding; M3 `#publish`/`#relay` facets must bind their own.
- Two unchosen numbers, no bead: `DefaultMailbox = 64`, renewal-beat
  fraction.

## Suggested next steps

- Root `359.8.5` may need `cert.Cav.Target` to admit `group:<name>`
  (kind-wide grants to receivers whose NodeIds git cannot enumerate).
  If so: `authorize.qnt` first, then `cert`, per the model-leads rule.
- M3 `0bc.3` (lighthouse as a plain actor) stays unblocked on the
  protocol side.
