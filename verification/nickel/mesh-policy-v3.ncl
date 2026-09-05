# mesh-policy-v3.ncl — snapshot invariants for the Mesh v3 policy
# recipe (talos-config-6z9, part (a); ADR-0017).
#
# Sibling of mesh-policy.ncl, NOT its replacement: the v2 (port-based)
# contract keeps validating the real talos/mesh-policy.yaml until the
# nebula render is deleted in Phase 4. This file validates the
# facet-class shape the recipe takes under ADR-0017, where the hub
# compiles the recipe into `invoke` grants that callers carry and a
# receiver holds nothing but its own accept table. Today it runs
# against fixtures/mesh-policy-v3.yaml, a spec fixture no code consumes.
#
# Scope — part (a) only. The compiled artifacts (per-receiver accept
# tables `facet → forward`, the name map) wait on 359.8.5's schema and
# have no contract here yet.
#
# Sources of truth for what is enforced:
#   - docs/desired-state/domain-model.md glossary: Facet (closed per
#     receiver kind; reachability is not a facet; ports never appear in
#     a grant), Group (a name, no semantics), Grant (`aud` is an actor
#     or a group name).
#   - docs/technical/adrs/0017-*.md Decision Outcome.
#   - config-server/mesh/nebmachine_test.go: machines must not reach
#     each other's control surfaces — carried from v2 as a contract.
#
# Contract-per-invariant, each mutation-tested (list in check.sh):
#   ClosedKinds           Policy is a closed record {node, gateway, hub}.
#   Facet <kind>          closed facet set per kind, exactly the glossary.
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
# Check: ./check.sh  (nickel export check.ncl --field mesh_policy_v3)
let groups = ["admins", "media", "machines"] in

# The closed facet set per receiver kind — glossary "Facet", verbatim.
# Reachability (ICMP today) is deliberately absent: it is an
# unauthenticated ping, not something a grant names. Adding a facet
# here is a domain-model change (glossary + ADR-0017), not a policy
# edit.
let facets = {
  node = ["apid", "kube-api"],
  gateway = ["ingress-http", "jellyfin"],
  hub = ["hub-http", "relay"],
}
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

# Facet names allowed on one receiver kind. `apid` on a gateway, or
# `icmp`/`ping`/`any` anywhere, fails here.
let Facet = fun kind =>
  let allowed = facets."%{kind}" in
  std.contract.from_validator (fun f =>
    if std.is_string f && std.array.elem f allowed then
      'Ok
    else
      'Error {
        message = "%{std.serialize 'Json f} is not a facet of a %{kind}",
        notes = [
          "%{kind} facets are the closed set %{std.serialize 'Json allowed} (glossary \"Facet\")",
          "reachability is not a facet; ports never appear in a grant",
        ],
      }
  )
in

# Closed record (no `..`): `port` and `proto` are gone from the grant
# shape — ports live only inside a facet definition (the accept table)
# and the device-local map — so their presence, like any typoed key,
# fails loudly.
let RuleShape = fun kind =>
  {
    facet | Facet kind,
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

let Rule = fun kind =>
  std.contract.Sequence [RuleShape kind, ExactlyOneTarget]
in

# A receiver kind with no rules is legal: under ADR-0017 "no grants
# name this receiver" is exactly what an empty list compiles to, and
# reachability no longer rides on a rule (v2 needed at least the ICMP
# rule). Every kind must still be declared — `inbound: []` is an
# explicit "nobody", an absent key would be indistinguishable from a
# typo.
let Scope = fun kind =>
  {
    inbound | Array (Rule kind),
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
  # Closed record: the receiver kinds are exactly the actors that hold
  # accept tables. `device` is gone (a device only initiates; it has
  # no facets), and an unknown kind fails.
  Policy = {
    node | std.contract.Sequence [Scope "node", NodeIsolatesMachines],
    gateway | Scope "gateway",
    hub | Scope "hub",
  },
}
