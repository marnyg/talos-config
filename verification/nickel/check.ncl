# check.ncl — apply the contracts to the durable artifacts.
# check.sh exports one field at a time (`--field <name>`), so a blame
# in one section never masks the other. `nickel export` fails (with a
# blame trace) iff the value violates a snapshot invariant.
{
  # v3: the REAL talos/mesh-policy-v3.yaml (facet-class recipe,
  # ADR-0017; compiled to grants by config-server/policy).
  mesh_policy_v3 =
    (import "../../talos/mesh-policy-v3.yaml")
      | (import "./mesh-policy-v3.ncl").Policy,
}
