#!/usr/bin/env bash
# Check the durable artifacts against their snapshot contracts
# (talos-config-6z9). Sibling of ../quint/check.sh: quint verifies the
# design over all traces; this validates the actual files in git.
#
#   ./check.sh    == mesh-policy-v3 ==
#                 talos/mesh-policy-v3.yaml (the REAL v3 file, input of
#                 config-server/policy) against mesh-policy-v3.ncl: the
#                 facet-class recipe shape of ADR-0017 — closed
#                 receiver kinds, the facet vocabulary declared in the
#                 file's own `facets:` section (ADR-0048: {name, kind,
#                 port}, names disjoint across kinds, ports disjoint
#                 within a kind, every rule's facet declared), no ports
#                 in a grant, one-of host/group, closed groups, node
#                 facets never granted to `group: machines`.
#                 == mutants ==
#                 then the mutants below, each a sed over a copy of the
#                 real file in a temp dir, expected to blame (or, for
#                 the positive controls, to pass).
#
# nickel comes from the flake (`nix develop --impure`); it is not on
# PATH otherwise.
#
# CI: .github/workflows/verify.yml runs this on every push/PR (job
# `nickel`), with nickel taken from the flake's nixpkgs input. Keep
# this note and that workflow in sync.
#
# Every contract is mutation-tested: seeding the bug it guards against
# produces a contract blame. (The v2 contract, mesh-policy.ncl over the
# nebula render's talos/mesh-policy.yaml, went with nebula in Mesh v3
# Phase 4; its mutations 1-7 are in git history.)
#   v3 (mesh-policy-v3.ncl):
#     8. unknown receiver kind (`phone`)          Policy (closed)
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
#   vocabulary (ADR-0048):
#    20. `facets:` section omitted                Policy (required)
#    21. facet with `kind: phone`                 FacetRow kind
#    22. facet `port: 0`                          FacetRow port
#    23. facet `port: 70000`                      FacetRow port
#    24. facet row with `proto: tcp`              FacetRow (closed)
#    25. facet name declared twice (`ssh` on node) FacetTable names
#    26. port 3389 twice under gateway            FacetTable ports
#    27. rule names an undeclared facet (`rdp`
#        row kept, `rdp` facet removed)           Facet gateway
#   Positive controls: `hub: {inbound: []}` and `{facet: apid, host:
#   hub}` under node both pass (in the real file); a new facet
#   `{name: probe, kind: gateway, port: 4242}` plus a `{facet: probe,
#   group: admins}` row under gateway passes with no contract edit;
#   hub-http and ingress-http sharing port 80 across kinds passes (in
#   the real file).
set -euo pipefail
cd "$(dirname "$0")"

echo "== mesh-policy-v3 =="
nickel export check.ncl --field mesh_policy_v3 --format json > /dev/null
echo "ok: talos/mesh-policy-v3.yaml satisfies mesh-policy-v3.ncl"

echo "== mutants =="
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cp mesh-policy-v3.ncl "$tmp/"
cat > "$tmp/check.ncl" <<'EOF'
{ mesh_policy_v3 = (import "./mesh-policy-v3.ncl").Policy (import "./policy.yaml") }
EOF

# mutant <label> <expect: blame|pass> <sed script applied to the real file>
mutant() {
  local label=$1 expect=$2 script=$3
  sed -e "$script" ../../talos/mesh-policy-v3.yaml > "$tmp/policy.yaml"
  if cmp -s "$tmp/policy.yaml" ../../talos/mesh-policy-v3.yaml; then
    echo "FAIL: $label — sed matched nothing, the mutant is the real file" >&2
    exit 1
  fi
  if (cd "$tmp" && nickel export check.ncl --field mesh_policy_v3 --format json > /dev/null 2>&1); then
    got=pass
  else
    got=blame
  fi
  if [ "$got" != "$expect" ]; then
    echo "FAIL: $label — expected $expect, got $got" >&2
    exit 1
  fi
  echo "ok: $label ($expect)"
}

# `facets:` rows sit directly under the `facets:` line; `/^facets:/a`
# inserts a row at the head of the list.
mutant "20 facets section omitted"      blame '/^facets:/,/^node:/{/^node:/!d}'
mutant "21 facet kind phone"            blame '/^facets:/a\  - {name: probe, kind: phone, port: 4242}'
mutant "22 facet port 0"                blame '/^facets:/a\  - {name: probe, kind: gateway, port: 0}'
mutant "23 facet port 70000"            blame '/^facets:/a\  - {name: probe, kind: gateway, port: 70000}'
mutant "24 facet row proto key"         blame '/^facets:/a\  - {name: probe, kind: gateway, port: 4242, proto: tcp}'
mutant "25 facet name twice"            blame '/^facets:/a\  - {name: ssh, kind: node, port: 2222}'
mutant "26 port twice within a kind"    blame '/^facets:/a\  - {name: probe, kind: gateway, port: 3389}'
mutant "27 rule names undeclared facet" blame '/name: rdp, kind: gateway/d'
mutant "8 unknown receiver kind"        blame '$a\phone:\n  inbound: []'
mutant "9 apid under gateway"           blame 's/{facet: rdp, group: admins}/{facet: apid, group: admins}/'
mutant "12 port on a rule"              blame 's/{facet: rdp, group: admins}/{facet: rdp, group: admins, port: "80"}/'
mutant "14 host and group"              blame 's/{facet: rdp, group: admins}/{facet: rdp, group: admins, host: laptop}/'
mutant "16 typoed group"                blame 's/{facet: rdp, group: admins}/{facet: rdp, group: admin}/'
mutant "17 host any"                    blame 's/{facet: rdp, group: admins}/{facet: rdp, host: any}/'
mutant "18 machines on a node facet"    blame 's/{facet: apid, group: admins}/{facet: apid, group: machines}/'
mutant "positive: new facet probe + rule, no contract edit" pass \
  '/^facets:/a\  - {name: probe, kind: gateway, port: 4242}
/{facet: rdp, group: admins}/a\    - {facet: probe, group: admins}'
echo "ok: mutants"
