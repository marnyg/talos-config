# check.ncl — apply the contracts to the real policy file in git.
# `nickel export check.ncl` fails (with a blame trace) iff the actual
# talos/mesh-policy.yaml violates a snapshot invariant.
let m = import "./mesh-policy.ncl" in
(import "../../talos/mesh-policy.yaml") | m.Policy
