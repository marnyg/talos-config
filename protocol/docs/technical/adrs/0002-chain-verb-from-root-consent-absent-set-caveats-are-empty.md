# ADR-0002: A chain's verb is its root consent's verb; an absent set caveat is the empty set

- Status: Proposed _(owner rulings 2026-09-13 on threads `xwu` and
  `0lo` from the M2 build; recorded as decision bead `eak` (∅) and task
  `xwu` (verb), the latter model-first before M3 `0bc.3`)_
- Date: 2026-09-13
- Amends: ADR-0001 (the fold `VerifyChain` was specified and built with
  `can == invoke` hard-coded at three points; the caveat vocabulary v2
  left "absent `endpoints`" undefined)
- Related: `verification/quint/authorize.qnt`, `protocol/cert/authorize.go`
  (`verifyChain`, `chainUnder`, `linksTo`, `speaksFor`, `Attenuate`),
  root ADR-0017 (verb never carries its object; attenuation is
  field-wise intersection), protocol invariants 4 and 5, issues `xwu`,
  `0lo`, `eak`, `7n8`, `seb`

## Context and Problem Statement

M2 built one N-link chain verifier (ADR-0001), but for the one verb M2
needed: `verifyChain` accepts only receiver-signed `invoke` consents,
`chainUnder` rejects any link whose `can != invoke` (`ErrChainVerb`),
and `linksTo`/`speaksFor` require a speak-as whose `cav.verbs ∋ invoke`.
M3 introduces chains for `relay` and `publish` (a lighthouse selling
`#publish`, relays selling `#relay`) and would either duplicate the
fold per verb or generalise it. Separately, caveat vocabulary v2 added
`endpoints` as a set caveat with intersection semantics but never said
what an *absent* `endpoints` means — harmless for `invoke`, decisive
the moment a chain's `endpoints` gates anything (`reach-me-at`
attenuation, relay chains). Both questions had to be answered once,
in the model, before the first non-`invoke` chain exists.

## Decision Drivers

- Invariant 1 / goal "one primitive": one verifier for every verb, not
  one fold per verb.
- Invariant 4: verbs are a closed set and never carry their object;
  invariant 5: attenuation only, field-wise intersection, unknown ⇒
  reject.
- ADR-0001's discipline: the Quint model leads, Go follows 1:1 with
  rapid laws.
- Wire stability: `Decode` already normalises a missing set to `[]`
  (`nonNil`), so any "unconstrained" reading would need a new wire
  sentinel.

## Considered Options

### Verb along the chain

**Option A: verb = root consent's verb, uniform along the chain.**
`VerifyChain` takes the verb from each receiver-signed consent it
roots under; `Attenuate` already enforces `child.Can == parent.Can`,
so uniformity falls out of the fold — the only change is removing the
three `invoke` literals and requiring the aud-side / issuer-side
speak-as to cover *that* verb. `ErrChainVerb` becomes "differs from
the root consent's verb".

- Pros: no new mechanism; the existing 18 chain laws stay valid with
  `invoke` as one instance; `relay`/`publish`/`reach-me-at` chains
  reuse the fold unchanged.
- Cons: a speak-as must now list every verb its hot key presents
  (`cav.verbs ∋ relay` for a relay operator's hot key) — explicit, and
  the intended reading of ADR-0018's `verbs` caveat.

**Option B: mixed-verb chains** (e.g. a `publish` grant delegated as an
`invoke` on facet `publish`). Cons: makes the verb carry its object by
the back door and turns `Attenuate` into a verb-mapping table —
refutes invariant 4. Ruled out.

**Option C: keep `invoke` only; model other verbs as facets of
`invoke`.** Cons: `reach-me-at` records are self-signed claims about
the issuer, not receiver-rooted authority, and `member` is already a
distinct verb — the closed verb set exists for a reason; collapsing it
loses the discriminant relays and lighthouses key on. Ruled out.

### Absent set caveat

**Option D: absent = ∅.** A caveat is a constraint; the absence of a
permission is not a permission. Uniform with `target`, `facet`,
`groups`, `verbs` today (plain intersection in model and Go; `Decode`
normalises nil to `[]`).

- Pros: zero code change; attenuation-only stays a pure intersection;
  a parent link that wants to permit endpoints must enumerate them.
- Cons: a delegator cannot say "any endpoint the delegatee later
  advertises" — the price of attenuation-only, already paid for
  `target`/`facet`.

**Option E: absent = unconstrained.** Cons: needs a wire-visible
sentinel (`"*"`-like) since nil and `[]` are indistinguishable after
`Decode`; introduces a second meaning for absence beside every other
set caveat; a forgotten field would *widen* authority — fail-open.
Ruled out.

## Decision Outcome

Chosen: **A + D.** One fold, parameterised by the root consent's verb;
every set caveat, `endpoints` included, is ∅ when absent. Note for
implementers: a `reach-me-at` *location record* is the issuer's
self-signed claim about itself (`aud: "*"`) and is verified as a single
certificate at envelope step 4 — it never enters `VerifyChain`; this
ADR's verb rule bites `relay` and `publish` chains. The chain-length
cap remains a deployment parameter (decision `seb`), not part of the
verifier's semantics.

### Consequences

- `authorize.qnt` first: `verifyChain` takes the verb from the consent;
  `linksTo`/`speaksFor` check `cav.verbs ∋ verb`; one new law for a
  non-`invoke` chain. Then Go 1:1 (`xwu` acceptance).
- Speak-as delegations for hot keys that operate relays or lighthouses
  must list those verbs; starter kits and the hub's unseal speak-as
  (ADR-0018) enumerate `verbs` explicitly.
- No wire change for absent sets; documentation of `endpoints` in the
  domain model gains the ∅ sentence.

### Confirmation

Right when M3's first `relay`/`publish` chain verifies through the
unchanged fold with only a verb parameter, and the mutation sweep
(`TestFaultPairSweep`) kills a "wrong-verb link accepted" mutant.
Invalidated if a legitimate delegation needs "any endpoint" semantics
that enumeration cannot express — then a sentinel must be designed as
a vocabulary bump, not read into absence.
