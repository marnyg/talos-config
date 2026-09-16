# ADR-0003: A receiver answers for a principal it holds a live `speak-as` from

- Status: Accepted _(ruled 2026-09-16 in the talos-config `359.8.2.1`
  grill-design; built 2026-09-18, task `kau`, model first)_
- Date: 2026-09-16 (accepted 2026-09-18)
- Amends: ADR-0001 (`VerifyChain` rule 4: `eff.Target ∋ receiver`)
- Related: root ADR-0018 (the `speak-as` verb; a long-lived root's hot
  key rotates per process), root ADR-0024 (the consumer: grants to hub
  facets name the sovereign), `verification/quint/authorize.qnt`,
  `protocol/cert/authorize.go` (`chainUnder`), issues `kau`, `xwu`, `7ei`

## Context and Problem Statement

`speak-as` (root ADR-0018) maps a *signer* to a principal: treat what
the hot key signs as the cold root's, within `cav`, until `exp`. It
says nothing about *receiving*. `VerifyChain` rule 4 requires the
effective target to contain the receiver's own id, so a grant to a
facet served by a rotating hot key must name the **current** hot key.
When the key rotates (every deploy, for the talos hub), every grant
that reaches it dies, and the facet that would hand out fresh grants is
behind one of those grants. The first message after a rotation cannot
be authorized.

The protocol's own model says a long-lived root is a cold key whose
facets are *served* by hot keys. Grants should be able to name the
root.

## Decision Drivers

- Protocol invariant: authority over an actor originates at that actor
  (its consents root every chain) — the extension must not let a
  caller widen the receiver's target set.
- Root ADR-0018's outcome — kill any hot-key process and lose nothing
  but in-flight delegations — must survive the rotation itself.
- One fold for every verb (ADR-0002); no special-cased facet.

## Considered Options

- **Grants name the hot key; a bootstrap facet re-issues them on a
  member cert alone** — rejected: the "any cert I signed authorizes
  asking" special case the glossary forbids for `#renew`.
- **Stable hot key** — rejected by root ADR-0018.
- **Receiver-held `speak-as` extends the receiver's target set
  (chosen).** Rule 4 becomes: `eff.Target ∋ R`, **or** `eff.Target ∋ P`
  where R holds a live `speak-as` P→R **in its own configuration** —
  never taken from the caller's bundle. The same cert that lets R sign
  as P lets R be addressed as P.
- **Also resolve envelope `to.target` and the dial id through the
  `speak-as`** — rejected: an `ed:` id is the iroh `EndpointId`; the
  transport pin and the Reply `from == to.target` check depend on it,
  and an `eth:` root has no endpoint. The caller dials and addresses
  the hot key it learned from the `speak-as`; only the **grant** names
  the principal.

## Decision Outcome

`VerifyChain(r cert.Receiver, verb, chain, speakAs, signer, facet, now)`
takes the receiver's own configuration as ONE value — `Receiver{ID,
Consents, SpeakAs}` — where `SpeakAs` is the set of `speak-as` certs R
*holds* naming `ID` as `aud` (an actor passes its whole `Actor.SpeakAs`;
the verifier filters). Rule 4's target test becomes `answersFor`:
`eff.Target ∋ R`, or `eff.Target ∋ P` for any P with a live such cert
at `now`. The consent rooting the chain is still the receiver's own
(`iss == receiver`); its target set may name P as well as R. Everything
else in the fold is unchanged.

Refinements ruled while building (2026-09-18):

- **The receiver-held set is a type, not a second slice.** Two adjacent
  `[]Cert` parameters with opposite trust roles (held vs. caller bundle)
  would compile when swapped — and the swap *is* this ADR's mutant.
  `cert.Receiver` makes "receiver-held" a compile-time distinction;
  `envelope.Receiver` gains `SpeakAs` and the `ChainVerifier` contract
  takes `cert.Receiver`. Precomputing a `[]ActorID` of principals was
  rejected: liveness would be judged outside the pure function.
- **Liveness only — no `cav.verbs` condition on the held cert.**
  `cav.verbs` says what R may *sign* as P (root ADR-0018); being
  addressed is not signing. A held `speak-as` covering `member` alone
  still lets R be addressed as P for an `invoke` chain (witnessed).
- **Same predicate in Authorize step 2b.** The consent that vouches for
  a member cert must name R *or a principal R answers for* — one rule,
  one helper (`consentTargets` → `answersFor`), so a hub running the
  per-connect check cannot contradict its own envelope path.
- **The held `speak-as` does not feed the clock mark.** It is `iss: P`,
  not R-signed; the rooting rule (signature-only provenance, ADR-0019)
  is untouched.

Model (`authorize.qnt`): `answersFor(r, held, target, now)`; scenario
var `held`; faults `FChainTargetOwner1`, `FGrantTargetOwner1`,
`FHeld{Missing,Expired,Forged,FromOwner2,AudOtherR,InBundleOnly}`;
laws `invChainTargetsAnswerable`, `invBundleSpeakAsNeverWidensTarget`,
`invTargetIsAnswerable`, `invConsentTargetsSelf` (generalised);
witness `answersForTest`. Mutants "held ∪ caller bundle", "liveness
dropped", "aud check dropped" all die (`quint run`, and the Go sweeps
`TestChainFaultPairSweep` × held faults / `TestFaultPairSweep`). Go
1:1 in `cert/authorize.go`; `TestVerifyChainAnswersFor` is the witness.

### Consequences

- Grants for facets served by a rotating hot key name the root
  (`target: wallet` in talos); a bundle never contains a hot-key
  target.
- `Actor.SpeakAs` already holds both roles of `speak-as` cert; the
  aud-side set is now an input to authorization (via
  `envelope.Receiver.SpeakAs`), not only to `#renew`'s own-issuance
  check.
- `ErrTargetMismatch` reads "the effective target omits the receiver
  and every principal it speaks for".
- `cert.Input.Receiver` is a `cert.Receiver` (was `ActorID` +
  `Consents`); no caller outside `protocol/` existed yet.
- `quint verify authorize.qnt --max-steps=2` is ~170 s (was ~135 s):
  `cav.target` may now name OWNER1 as well as a receiver.

### Confirmation

Right if: after a hot-key rotation, a caller holding a grant `target:
P` reaches the new hot key's facets with no new cert; a caller
presenting a `speak-as` P→R that R does not hold is refused with
`ErrTargetMismatch`. Wrong if any verifier needs to know the current
hot key to *issue* a grant.
