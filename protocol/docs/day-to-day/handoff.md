# Handoff — sovereign-actor protocol

<!-- "Where we left off" for the protocol scope. Overwritten per session.
     Deployment (talos/hub/fly) context lives in the root handoff. -->

## Last session

2026-09-17 — **`xwu` built: the chain's verb is the root consent's
verb (ADR-0002 → Accepted). Two commits, `9bf48ac` + `e158fbc`.**

- **Model first** (`verification/quint/authorize.qnt`):
  `verifyChain(r, verb, …)` roots only in R's consents with
  `can == verb`; `chainUnder` reads the verb off the root and threads it
  through `linkStep`/`resolve`/`audBinding` — no `Invoke` literal left in
  the fold. New `Publish` verb, `cverb` scenario var (both `gen` and
  `genNear` draw `invoke | publish`), faults `FLink1VerbOtherChain`,
  `FAudSpeakAsNoVerb`, `FHubBSpeakAsVerbless`; laws
  `invChainVerbIsRoot` (was `…IsInvoke`), new `invChainVerbUniform`,
  `invAudSpeakAsCoversVerb` (was `…NeedsInvoke`); witness
  `publishChainTest`. Mutants "root filter forgets the verb" and "link
  verb check dropped" both die under `invAll`. `attenuate` now also
  rejects a verb change (1:1 with Go `ErrVerbMismatch`).
- **Go 1:1**: `cert.VerifyChain(receiver, verb, …)`; `ErrChainVerb` =
  "differs from the root consent verb"; `speaksFor(verb)`; `Authorize`
  is the `invoke` instance; `actor.invokeChain` binds `VerbInvoke`
  (`envelope.ChainVerifier` unchanged — an envelope IS an invocation).
  Rapid laws renamed to match; `buildConsents(…, chainVerb)` adds a
  `publish` twin of consent1; `TestChainFaultPairSweep` ×
  `{invoke, publish}`; `TestVerifyChainPublish`. `go test ./...` green.
- **Refinement recorded in ADR-0002**: the *receiver names the verb it
  expects* (parameter) rather than inferring it from whichever consent
  matches — otherwise a `publish` consent could root an `invoke`
  operation (verb confusion, fail-open).

## Loose threads

- `kau` (rule 4, ADR-0003) next: same `chainUnder`/`speaksFor` seam;
  the receiver-held speak-as is a NEW input to `VerifyChain` (never the
  caller's bundle) — decide the parameter shape before editing the
  model. ADR-0003 → Accepted when it lands; then amend the glossary
  *Authorize* ("require target ∋ R").
- Glossary *Authorize* could say "the `invoke` instance of
  `VerifyChain`" — proposed at wrap-up, not written.
- M3 (`0bc.3`) facets will bind their own verb: which facet expects
  which verb is a per-actor table the runtime does not have yet
  (`actor.invokeChain` is the only binding).
- `quint verify authorize.qnt --max-steps=2` is now ~135 s (was ~95 s;
  `check.sh` note refreshed). Nightly tier only.
- Two unchosen numbers, no bead: `DefaultMailbox = 64`, renewal-beat
  fraction.

## Suggested next steps

- `kau`: `authorize.qnt` first — new law + the mutant "caller-bundle
  speak-as never widens the receiver's target set" — then Go
  (`chainUnder` rule 4 takes the receiver's aud-side `SpeakAs`); run
  `TestFaultPairSweep` + `TestChainFaultPairSweep`.
- Then `7ei` in the actor runtime (`renew.go`: resolve `old.Aud` via
  `speaksFor`, verb `invoke`).
