#!/usr/bin/env bash
# Check the durable artifacts against their snapshot contracts
# (talos-config-6z9). Sibling of ../quint/check.sh: quint verifies the
# design over all traces; this validates the actual files in git.
#
#   ./check.sh    == mesh-policy-v3 ==
#                 talos/mesh-policy-v3.yaml (the REAL v3 file, input of
#                 config-server/policy) against mesh-policy-v3.ncl: the
#                 facet-class recipe shape of ADR-0017 — closed
#                 receiver kinds, closed facet set per kind (relay is
#                 not a facet), no ports in a grant, one-of host/group,
#                 closed groups, node facets never granted to
#                 `group: machines`.
#
# nickel comes from the flake (`nix develop --impure`); it is not on
# PATH otherwise.
#
# CI: .github/workflows/verify.yml runs this on every push/PR (job
# `nickel`), with nickel taken from the flake's nixpkgs input. Keep
# this note and that workflow in sync.
#
# Every contract has been mutation-tested: seeding the bug it guards
# against produces a contract blame. (The v2 contract, mesh-policy.ncl
# over the nebula render's talos/mesh-policy.yaml, went with nebula in
# Mesh v3 Phase 4; its mutations 1-7 are in git history.)
#   v3 (mesh-policy-v3.ncl):
#     8. unknown receiver kind (`device`)         Policy (closed)
#     9. `facet: apid` under gateway              Facet gateway
#    10. `facet: icmp` under hub (reachability)   Facet hub
#    10b. `facet: relay` under hub (transport)    Facet hub
#    11. `facet: any` under node                  Facet node
#    12. `port: "80"` on a rule                   RuleShape (closed)
#    13. `proto: tcp` on a rule                   RuleShape (closed)
#    14. host + group on one rule                 ExactlyOneTarget
#    15. neither host nor group                   ExactlyOneTarget
#    16. typoed group (`admin`)                   Group
#    17. `host: any`                              Host
#    18. `group: machines` on a node facet        NodeIsolatesMachines
#    19. receiver kind omitted (`hub` missing)    Policy (required)
#   Positive controls: `hub: {inbound: []}` and `{facet: apid, host:
#   hub}` under node both pass.
set -euo pipefail
cd "$(dirname "$0")"

echo "== mesh-policy-v3 =="
nickel export check.ncl --field mesh_policy_v3 --format json > /dev/null
echo "ok: talos/mesh-policy-v3.yaml satisfies mesh-policy-v3.ncl"
