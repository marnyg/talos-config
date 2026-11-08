# mesh-policy-v3.ncl — snapshot invariants for the Mesh v3 policy
# recipe (talos-config-6z9, part (a); ADR-0017).
#
# Successor of mesh-policy.ncl (the v2 port-based contract, deleted
# with the nebula render in Phase 4). This file validates the
# facet-class shape the recipe takes under ADR-0017, where the hub
# compiles the recipe into `invoke` grants that callers carry and a
# receiver holds nothing but its own accept table. It validates the
# REAL talos/mesh-policy-v3.yaml, the input of config-server/policy
# (359.8.5), whose Go loader enforces the same rules — the two must
# agree (policy_test.go runs the same mutants against policy.Parse).
#
# Scope: the recipe only. Under the 2026-09-18 amendment the hub
# renders no accept table and the name map waits on e8d, so there is
# no compiled artifact to contract here.
#
# Sources of truth for what is enforced:
#   - docs/desired-state/domain-model.md glossary: Facet (declared in
#     the recipe's `facets:` section since ADR-0048, disjoint names
#     across receiver kinds; reachability is not a facet; ports never
#     appear in a grant), Group (a name, no semantics), Grant (`aud` is
#     an actor or a group name).
#   - docs/technical/adrs/0017-*.md Decision Outcome; 0048 (vocabulary
#     as data).
#   - config-server/mesh/nebmachine_test.go: machines must not reach
#     each other's control surfaces — carried from v2 as a contract.
#
# Contract-per-invariant, each mutation-tested (list in check.sh):
#   ClosedKinds           Policy is a closed record {facets, node, gateway,
#                         hub, device}.
#   FacetRow              closed record {name, kind, port}: kind ∈ kinds,
#                         port 1–65535, non-empty name.
#   FacetTable            names disjoint across the table; ports disjoint
#                         within a kind.
#   Facet <kind>          a rule's facet is declared under `facets:` with
#                         this kind.
#   RuleShape             closed record {facet, host|group}: `port`/`proto`
#                         or any typoed key ⇒ blame.
#   ExactlyOneTarget      exactly one of host/group.
#   Group                 closed set {admins, media, machines}.
#   Host                  a member *name*; never "any" (a grant always
#                         names its `aud`).
#   NodeIsolatesMachines  no `group: machines` rule under `node`.
#
# House-style note: v2 uses `from_predicate`; here `from_validator` is
# used where it lets the blame name the invariant it broke (a facet
# from the wrong kind, `host: any`, machines on a node) instead of the
# bare "contract broken".
#
# `Policy` is a function of the document: the per-kind Facet contracts
# are built from the document's own `facets:` section, so check.ncl
# applies it as `Policy recipe` rather than `recipe | Policy`.
#
# Check: ./check.sh  (nickel export check.ncl --field mesh_policy_v3)
let groups = ["admins", "media", "machines"] in

let kinds = ["node", "gateway", "hub", "device"] in

# One `facets:` row (ADR-0048): a facet name, the receiver kind whose
# vocabulary owns it, and its natural port. Reachability (ICMP) is
# deliberately not a facet: it is an unauthenticated ping, not something
# a grant names. The iroh relay is NOT a facet (2026-09-18 amendment):
# it is a keyless transport child, no grant can be presented to it. The
# Issuer's actor facets (#renew, mint-device) are granted by the Kit and
# the enrollment flow, never by a recipe row.
let FacetRow = {
  name
    | std.contract.from_validator (fun n =>
      if std.is_string n && n != "" then
        'Ok
      else
        'Error { message = "facet name must be a non-empty string" }
    ),
  kind
    | std.contract.from_validator (fun k =>
      if std.is_string k && std.array.elem k kinds then
        'Ok
      else
        'Error {
          message = "unknown receiver kind %{std.serialize 'Json k}",
          notes = ["kinds are the closed set %{std.serialize 'Json kinds}"],
        }
    ),
  port
    | std.contract.from_validator (fun p =>
      if std.is_number p && p == std.number.floor p && p >= 1 && p <= 65535 then
        'Ok
      else
        'Error { message = "port must be an integer in 1–65535, got %{std.serialize 'Json p}" }
    ),
}
in

# The whole table: names disjoint across kinds (a grant's reach is its
# facet's — kind ≡ facet vocabulary) and ports disjoint within a kind
# (a port is read in the vocabulary of the name's kind: hub-http and
# ingress-http may both be 80).
let FacetTable =
  std.contract.from_validator (fun rows =>
    let names = std.array.map (fun r => r.name) rows in
    let dupNames =
      names |> std.array.filter (fun n => std.array.length (std.array.filter (fun m => m == n) names) > 1)
    in
    let dupPorts =
      rows
      |> std.array.filter (fun r =>
        std.array.length (std.array.filter (fun s => s.kind == r.kind && s.port == r.port) rows) > 1
      )
    in
    if dupNames != [] then
      'Error {
        message = "facet names declared twice: %{std.serialize 'Json (std.array.sort std.string.compare dupNames)}",
        notes = ["facet names are disjoint across kinds (kind ≡ facet vocabulary)"],
      }
    else if dupPorts != [] then
      'Error {
        message = "natural port shared within a kind: %{std.serialize 'Json dupPorts}",
        notes = ["ports are disjoint within a kind (a presentation reads a port in the kind's vocabulary)"],
      }
    else
      'Ok
  )
in

# The names declared under `facets:` for one kind.
let facetsOf = fun table kind =>
  table
  |> std.array.filter (fun r => r.kind == kind)
  |> std.array.map (fun r => r.name)
in


# Same closed group set as v2: a typoed group would compile to grants
# addressed to nobody.
let Group =
  std.contract.from_validator (fun g =>
    if std.is_string g && std.array.elem g groups then
      'Ok
    else
      'Error {
        message = "unknown group %{std.serialize 'Json g}",
        notes = ["groups are the closed set %{std.serialize 'Json groups}"],
      }
  )
in

# `host` is a member name (the `aud` of a single-actor grant). "any" is
# rejected: under ADR-0017 there is no grant without an `aud`, and the
# one v2 use of `host: any` (ICMP reachability) is not a facet.
let Host =
  std.contract.from_validator (fun h =>
    if std.is_string h && h != "" && h != "any" then
      'Ok
    else
      'Error {
        message = "host must be a member name, got %{std.serialize 'Json h}",
        notes = ["a grant always names its `aud`; `any` is not an actor (ADR-0017)"],
      }
  )
in

# Facet names allowed on one receiver kind: the ones `facets:` declares
# with that kind. `apid` on a gateway, an undeclared `probe`, or
# `icmp`/`ping`/`any` anywhere, fails here.
let Facet = fun table kind =>
  let allowed = facetsOf table kind in
  std.contract.from_validator (fun f =>
    if std.is_string f && std.array.elem f allowed then
      'Ok
    else
      'Error {
        message = "%{std.serialize 'Json f} is not a facet of a %{kind}",
        notes = [
          "%{kind} facets declared under `facets:` are %{std.serialize 'Json allowed} (glossary \"Facet\")",
          "reachability is not a facet; ports never appear in a grant",
        ],
      }
  )
in

# Closed record (no `..`): `port` and `proto` are gone from the grant
# shape — ports live only inside a facet definition (the accept table)
# and the device-local map — so their presence, like any typoed key,
# fails loudly.
let RuleShape = fun table kind =>
  {
    facet | Facet table kind,
    host | Host | optional,
    group | Group | optional,
  }
in

# Exactly one of host/group per rule: a grant has one `aud`.
let ExactlyOneTarget =
  std.contract.from_validator (fun r =>
    if std.record.has_field "host" r != std.record.has_field "group" r then
      'Ok
    else
      'Error {
        message = "a rule names exactly one of host/group (the grant's `aud`)",
      }
  )
in

let Rule = fun table kind =>
  std.contract.Sequence [RuleShape table kind, ExactlyOneTarget]
in

# A receiver kind with no rules is legal: under ADR-0017 "no grants
# name this receiver" is exactly what an empty list compiles to, and
# reachability no longer rides on a rule (v2 needed at least the ICMP
# rule). Every kind must still be declared — `inbound: []` is an
# explicit "nobody", an absent key would be indistinguishable from a
# typo.
let Scope = fun table kind =>
  {
    inbound | Array (Rule table kind),
  }
in

# Design stance carried from v2 (nebmachine_test): machines must not
# reach each other's control surfaces — a compromised node's blast
# radius stops at itself. In grant terms: no `invoke` grant with
# `aud: group:machines` may target a node facet.
let NodeIsolatesMachines =
  std.contract.from_validator (fun scope =>
    let offending =
      scope.inbound
      |> std.array.filter (fun r =>
        std.record.has_field "group" r && r.group == "machines"
      )
    in
    if offending == [] then
      'Ok
    else
      'Error {
        message = "node facets are never granted to `group: machines`",
        notes = [
          "offending rule(s): %{std.serialize 'Json offending}",
          "machines must not reach each other's control surfaces (nebmachine_test)",
        ],
      }
  )
in

{
  # Closed record: the receiver kinds are exactly the facet
  # vocabularies, and `facets:` is the vocabulary itself (ADR-0048).
  # `device` returned 2026-10-17 (ADR-0038) as the vocabulary of what a
  # device may serve — a kind is not a kind of member. An unknown kind
  # fails. Applied as `Policy recipe` (check.ncl): the rule contracts are
  # built from the document's own table, so an undeclared facet in a
  # rule blames the rule, not a compiled-in list.
  Policy = fun recipe =>
    let table = recipe.facets | Array FacetRow | FacetTable in
    recipe
      | {
        facets | Array FacetRow | FacetTable,
        node | std.contract.Sequence [Scope table "node", NodeIsolatesMachines],
        gateway | Scope table "gateway",
        hub | Scope table "hub",
        device | Scope table "device",
      },
}
