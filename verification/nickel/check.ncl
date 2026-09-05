# check.ncl — apply the contracts to the durable artifacts.
# check.sh exports one field at a time (`--field <name>`), so a blame
# in one section never masks the other. `nickel export` fails (with a
# blame trace) iff the value violates a snapshot invariant.
{
  # v2: the REAL talos/mesh-policy.yaml (port-based, nebula render).
  mesh_policy =
    (import "../../talos/mesh-policy.yaml")
      | (import "./mesh-policy.ncl").Policy,

  # v3 part (a): the facet-class recipe shape (ADR-0017), against a
  # spec fixture until 359.8.5 lands the schema in talos/.
  mesh_policy_v3 =
    (import "./fixtures/mesh-policy-v3.yaml")
      | (import "./mesh-policy-v3.ncl").Policy,
}
