# ADR-0003: A receiver answers for a principal it holds a live `speak-as` from

- Status: Proposed _(ruled 2026-09-16 in the talos-config `359.8.2.1`
  grill-design; task `kau`, model-first before Mesh v3 Phase 1.1)_
- Date: 2026-09-16
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

`VerifyChain(receiver, consents, chain, speakAs, …)` takes the
receiver's own `speak-as` set (the `Actor.SpeakAs` certs naming the
receiver as `aud`) and accepts `eff.Target ∋ P` for any P with a live
such cert at `now`. The consent rooting the chain is still the
receiver's own (`iss == receiver`); its target set may name P as well
as R. Everything else in the fold is unchanged.

Model first: `authorize.qnt` gains a law (a grant targeting P is
accepted by R holding a live `speak-as` P→R) and a mutant (the same
`speak-as` presented only in the caller's bundle is rejected — a
stranger's `speak-as` to R must never make R answer for a wallet the
receiver never consented to). Then Go 1:1, rapid laws, `xwu`/`7ei`
tests green.

### Consequences

- Grants for facets served by a rotating hot key name the root
  (`target: wallet` in talos); a bundle never contains a hot-key
  target.
- `Actor.SpeakAs` already holds both roles of `speak-as` cert; the
  aud-side set becomes an input to authorization, not only to
  `#renew`'s own-issuance check.
- `ErrTargetMismatch` doc: "the effective target omits the receiver
  and every principal it speaks for".

### Confirmation

Right if: after a hot-key rotation, a caller holding a grant `target:
P` reaches the new hot key's facets with no new cert; a caller
presenting a `speak-as` P→R that R does not hold is refused with
`ErrTargetMismatch`. Wrong if any verifier needs to know the current
hot key to *issue* a grant.
