# Handoff — sovereign-actor protocol

<!-- "Where we left off" for the protocol scope. Overwritten per session.
     Deployment (talos/hub/fly) context lives in the root handoff. -->

## Last session

2026-09-18 — **The last two verifier rulings landed: `kau` (rule 4,
ADR-0003 → Accepted) and `7ei` (`#renew` aud binding). Commits
`a2b0703`, `df7e1d5`, `e469679`, `1f8865a`.**

- **`kau`, model first** (`verification/quint/authorize.qnt`):
  `answersFor(r, held, target, now)` — `target ∋ R` or `∋ P` with a live
  `speak-as P→R` in what R *holds*; used by `effAdmits` (rule 4) and
  `memberSovereigns` (step 2b). New scenario vars `held`/`heldAtt`;
  `cav.target` may name `OWNER1`; faults `FChainTargetOwner1`,
  `FGrantTargetOwner1`, `FHeld{Missing,Expired,Forged,FromOwner2,
  AudOtherR,InBundleOnly}`; laws `invChainTargetsAnswerable`,
  `invBundleSpeakAsNeverWidensTarget`, `invTargetIsAnswerable`;
  witness `answersForTest`. Mutants "held ∪ caller bundle", "liveness
  dropped", "aud check dropped" die in Quint and in the Go sweeps.
- **Go 1:1**: `cert.Receiver{ID, Consents, SpeakAs}` groups the
  receiver-held inputs — held vs caller-presented speak-as is a *type*,
  not two adjacent `[]Cert`. `VerifyChain(r Receiver, verb, chain,
  speakAs, signer, facet, now)`; `Input.Receiver` is a `Receiver`;
  `envelope.Receiver`/`ChainVerifier` carry `SpeakAs`; `actor` passes
  `a.SpeakAs`. Rulings recorded in ADR-0003: no `cav.verbs` condition
  on the held cert (being addressed ≠ signing), same predicate in
  Authorize 2b, held cert does not feed the mark.
- **`7ei`**: `cert.SpeaksFor` exported (rule 3 as a predicate);
  `renewOne` accepts `old.aud == from` *or* a live `speak-as
  aud→from` in the proof with `verbs ∋ invoke`. `RefuseAud` reworded.
  `TestRenewViaHolderHotKey`. Glossary *Renewal beat* updated.
- `quint verify authorize.qnt --max-steps=2` ≈ 165–170 s (was ~135 s).

## Loose threads

- The held `speak-as` is receiver configuration but not R-signed, so
  it stays out of `Result.Verified`/`clock.Mark` (ADR-0003). If a hub
  ever needs its own `speak-as`'s `iat` to advance the mark, that is a
  clock.qnt question, not an authorize one.
- No facet→verb table yet: `actor.invokeChain` is the only verb
  binding; M3 `#publish`/`#relay` facets must bind their own.
- Two unchosen numbers, no bead: `DefaultMailbox = 64`, renewal-beat
  fraction.

## Suggested next steps

- Protocol side is clear for talos `359.8.1` (membership issuance,
  ADR-0018/0024): the hub's consents name `{hubkey, wallet}`, grants to
  hub facets name `target: wallet`, the hub passes its unseal
  `speak-as` as `Receiver.SpeakAs`.
- M3 `0bc.3` (lighthouse as a plain actor) is unblocked on the protocol
  side if you'd rather stay in `protocol/`.
