# ADR-0048: The facet vocabulary is recipe data; a receiver accepts exactly what it forwards or serves

- Status: Proposed 2026-11-08 (swarm cut 2+3 of `talos-config-m6xc`'s
  follow-through; `talos-config-4rqw`). Not yet deployed: the hub must
  pick up `policy` before the recipe with `facets:` reaches the tip
  (ordering below).
- Date: 2026-11-08
- Related: ADR-0017 (+ 2026-09-18 amendment: kind ≡ facet vocabulary —
  kept for grants), ADR-0046 (the facet whose arrival cost a hub
  unseal, a gateway image and a device rebuild), ADR-0047 (cut 1:
  presentations read the port off the advertisement; freed the
  devices), ADR-0038 (a device that serves is a receiver).

## Context and Problem Statement

After ADR-0047 a new facet still cost two rebuilds. The hub's
`policy.Load` rejected a recipe row naming a facet outside a compiled
`facets` map (`"%q is not a facet of a %s (closed set …)"`), so the
recipe could not mention `syncthing` until a hub carrying the word
was deployed and unsealed. The gateway took one named flag per raw-TCP
facet (`-rdp`, `-syncthing`), and `nodeagent.Start` refused a
`Forward`/`Serve` key outside `policy.Facets(kind)`, so the receiver
could not serve the facet until its image carried the word too. Both
checks guarded nothing: the hub compiles a row into a wildcard grant
whatever the name, and a receiver's consent is its own opt-in.

## Decision Drivers

- A facet is an Owner's decision about the recipe, so it should land
  where the Owner's other decisions land: a signed git tip the hub
  reads on its poll — no image, no unseal.
- The receiver is the authority on what it serves; the recipe on who
  may reach it. Neither needs a third party's word list.
- Invariants 1–3 untouched: `cert.Authorize` still admits only a
  signed grant whose facet the receiver consented to.
- Mixed fleet for weeks: an old hub, an old gateway, an old device
  must each survive the new recipe or the new binaries.

## Considered Options

- **A — keep the compiled tables, bump them per facet.** The status
  quo this ADR records.
- **B — ship the vocabulary in the `#bundle`.** Rejected in ADR-0047
  for the port; same reasons for the names.
- **C — `facets:` as a section of the recipe; receivers accept what
  they forward/serve; compiled tables stay as the device-side
  default.** Chosen.

## Decision Outcome

**Vocabulary as data.** `talos/mesh-policy-v3.yaml` gains a top-level
`facets:` list of `{name, kind, port}` rows — the seven of today.
`policy.Parse` decodes it (strict fields), `Vocabulary.Validate`
enforces kind ∈ Kinds, non-empty name, port 1–65535, names disjoint
across the table, ports disjoint within a kind; every rule's facet
must be declared for its kind. `Recipe.Vocabulary()` exposes it. The
Nickel contract (`verification/nickel/mesh-policy-v3.ncl`) enforces
the same rules, built from the document's own section (`Policy` is
now a function of the recipe), and `check.sh` runs mutants 20–27 plus
the positive control: a `probe` facet and a `{facet: probe, group:
admins}` row pass with no contract edit. `policy_test.go` runs the
same mutants against `Parse` over the live file — the regex mirror of
the `.ncl` is gone.

**Producer-derived accept tables.** `nodeagent.Start` builds its
accept table, ALPNs and reach-me-at from exactly `Forward ∪ Serve`
(`receiverTable`); the kind is validated but no longer gates names.
`cmd/gateway` takes `-forward facet=host:port` (repeatable) in place
of `-rdp`/`-syncthing`; `k8s/apps/gateway/deployment.yaml` passes two
such args. A facet the receiver forwards but the recipe never names is
advertised and accepted but reached by nobody: no grant compiles.

**What stays compiled.** `policy.Default` — the same seven rows — is
the vocabulary of a member with no recipe at hand: irohup's bridge
defaults and `-forward` guard, the node agent's natural-port lookup
(`FacetPort`), `zone.KindOf`/`FacetByPort` on peers, and tests.
`TestDefaultMatchesShippedRecipe` pins it to the shipped section until
the device side reads the vocabulary off the plane (a follow-up, not
this cut). The hub's own facets (`hubfacet.go`, `hub-http`) stay
compiled: the hub is its own producer and deployer. The group and kind
sets stay compiled on both sides (`TestGroupsMatchNickel`).

### Ordering hazard: an old hub reading the new recipe

`policy.Parse` uses `yaml.Decoder.KnownFields(true)`: a hub built
before this ADR fails `Load` on a recipe with `facets:` ("field facets
not found"), and the Issuer refuses every `#bundle` until the hub is
replaced — a fleet-wide grant outage one beat later. Sequence as
ADR-0046's deploy did: **hub image first** (it reads the old recipe
fine — no section ⇒ `Default`), then the recipe commit on the tip. Not
solved in code on purpose: a loose decoder would hide the next typo.
The gateway is the mirror image: the new deployment args need the new
image (an old binary rejects `-forward`), so the image pin and the
args land in one commit, which is the orchestrator's.

### Consequences

- Good: a new facet is a `facets:` row, a rule, and a `-forward` on
  the receiver that serves it. No hub rebuild, no unseal, no gateway
  image, no device rebuild (with ADR-0047).
- Good: `nodeagent.Start`'s table derivation is C-free and unit-tested
  (`receiver_test.go`); the gateway flag type is too.
- Neutral: a recipe without `facets:` still loads (`Default`), so old
  checkouts and test fixtures work; the Nickel contract requires the
  section in the shipped file.
- Bad / to watch: `Default` is a second copy of the shipped section,
  pinned by a test rather than derived. It goes when devices read the
  vocabulary from the plane — the follow-up that would also let a
  device forward a facet `Default` lacks (irohup's guard still reads
  it today).
- Bad / to watch: ports disjoint *within* a kind, not across (hub-http
  and ingress-http are both 80). A member advertising both vocabularies
  (ADR-0047 allows it) would collide at the presentation; the receiver
  is where to check that if it ever happens.
