# ADR-0004: `cav.target` admits the wildcard `"*"` — honored at every receiver that consented to the chain's sovereign

- Status: Proposed _(2026-09-18, from the `359.8.5` grill-design;
  promote when the model law and the Go port land)_
- Date: 2026-09-18
- Amends: ADR-0002 (an absent set caveat stays ∅; this adds the
  explicit wire sentinel ADR-0002 said an "unconstrained" reading
  would need — for `target` only). ADR-0001 (caveat vocabulary v2 →
  v3: `target` may contain `"*"`).
- Related: ADR-0003 (rule 4 is unchanged by this ADR), root ADR-0017
  (policy compiles to grants) and its 2026-09-18 amendment (kind ≡
  facet vocabulary), root ADR-0015 (identity keys are minted on the
  machine — the reason git cannot enumerate targets), invariant 5
  (attenuation only), `verification/quint/authorize.qnt`,
  `protocol/cert/{decode,authorize}.go`

## Context and Problem Statement

A grantor that compiles declared policy into grants must name each
grant's `cav.target`. In the first consumer (talos) a rule such as
`{facet: apid, group: admins}` means "every node's `apid`" — but node
keys are minted on the node (ADR-0015), so the compiler, which reads
git, cannot enumerate them. `Attenuate` intersects `target`
field-wise with the receiver's consent (`{target: [self]}`), and
ADR-0002 fixed an absent set as ∅, so a grant that names no target
reaches nothing. The primitive needed a way to say "every receiver
that has consented to this sovereign" without listing keys, and
without weakening rule 4 (a receiver answers only for itself and for
principals it holds a live `speak-as` from).

## Decision Drivers

- Invariant 5 (attenuation only, field-wise intersection, unknown ⇒
  reject): whatever is added must fold as an intersection.
- Invariant 2 (offline, receiver-rooted verification): the wildcard
  must not reintroduce a lookup or a registry of receiver keys.
- ADR-0003: rule 4 stays exactly as modelled; the sentinel must not
  become a second "answers-for" path.
- Precedent: `aud: "*"` already exists with a bounding condition
  (postage). The `target` sentinel's bound must be stated with the
  same care.
- The compiler must be a pure function of git (root invariant 2);
  grant contents cannot depend on a cache of who has beaten.

## Considered Options

**Option A: `target: group:<kind>` resolved against the receiver's own
member cert.** Symmetric to `aud: group:g` (caller side). Needs a
`Receiver.Member` input, a new rule in `authorize.qnt` and Go, and
buys per-receiver-group scoping nobody needs yet. Deferred, not
refuted: it is the upgrade path if a grant ever needs to be narrower
than "every receiver exposing this facet".

**Option B: per-receiver targets from the grantor's location cache.**
The compiler lists every receiver key it has seen beat. Cold cache
after every restart ⇒ empty targets ⇒ callers lose access for a poll
interval; a safe-to-lose cache becomes an authorization input through
the back door. Ruled out.

**Option C: `"*"` in `cav.target` (chosen).** A wire sentinel in the
`target` set: `intersect(X, {*}) = X`, `intersect({*}, {*}) = {*}`;
a `"*"` that survives to the effective cert is treated by rule 4 as
"names no one" (rule 4 tests `eff.target ∋ R or ∋ P` for concrete
ids only — so a chain whose *consent* also says `"*"` is rejected,
which is correct: a receiver that names no concrete self in its
consent has consented to nothing).

**Option D: generalise ADR-0003 so a receiver answers for the
sovereign it consented to** (`target: wallet` for every facet, consent
declares `[self, wallet]`). Weakens rule 4 from "P signed a speak-as
to R" to "R claims P in its own consent"; no gain over C. Ruled out.

## Decision Outcome

Chosen: **Option C.** Caveat vocabulary v3: the `target` set may
contain the string `"*"`.

- **Semantics**: `"*"` in a link's `target` means *the link does not
  narrow the target*. Attenuation: `intersect(parent, child)` where
  either side is `{*}` yields the other side; both `{*}` yields
  `{*}`. Mixed sets (`["*", ed:…]`) are rejected at decode — the
  wildcard is the whole set or absent.
- **Rule 4 unchanged**: the effective target must name the receiver
  (or a speak-as principal it holds) *concretely*. Because a
  receiver's own consent always names `self`, the effective target of
  `[consent, grant{target: *}]` is `[self]` and rule 4 passes; a
  consent with `target: "*"` fails rule 4 by construction.
- **Bound**: a `target: "*"` grant is honored at exactly the receivers
  that hold a live delegable consent to the grant's sovereign for the
  requested facet — no wider. It does not need postage: `aud "*"`
  widens *who may present* (spam ⇒ postage), `target "*"` widens
  nothing beyond what each receiver already consented to.
- **Not for other set caveats**: `facet`, `groups`, `verbs`,
  `endpoints` keep ADR-0002's ∅ reading with no sentinel. A grant
  must still name its facets.

### Consequences

- `authorize.qnt` first: `TARGETS` gains an `ANY` value; `attenuate`'s
  target case handles it; `answersFor` ignores it; new law
  `invWildcardTargetNeverWidens` (for every chain, admitting with a
  `*` link ⇒ admitting with that link's target replaced by the
  consent's own target). Then Go 1:1: `decode.go` accepts `"*"` as a
  target element only as the singleton set; `intersectID` handles the
  sentinel; a rapid law mirrors the Quint one.
- The first consumer's compiler (root `359.8.5`) emits
  `target: ["*"]` on every policy grant; the consumer's "kind" axis is
  carried by the facet vocabulary (root ADR-0017 amendment).
- Domain-model glossary (root and protocol) gains the sentence under
  **Grant** / **Attenuation**.

### Confirmation

Right if the Quint law holds and the consumer's round-trip suite
(`4un`) shows no receiver of kind K admitting a grant whose facet
belongs to kind K′ ≠ K. Wrong if any consumer needs `"*"` in a
second set caveat — that would be Option E of ADR-0002 (absent =
unconstrained) returning, and the sentinel should be reconsidered
rather than spread.
