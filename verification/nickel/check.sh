#!/usr/bin/env bash
# Check the real durable artifacts against their snapshot contracts
# (talos-config-6z9). Sibling of ../quint/check.sh: quint verifies the
# design over all traces; this validates the actual files in git.
#
#   ./check.sh    validates talos/mesh-policy.yaml against
#                 mesh-policy.ncl (schema + design invariants:
#                 closed groups, one-of host/group, node isolates
#                 machines, device inbound ICMP-only)
#
# Every contract has been mutation-tested: seeding the bug it guards
# against (machines group on node, tcp into a device, typoed group or
# key, out-of-range port, empty scope, host+group on one rule)
# produces a contract blame.
set -euo pipefail
cd "$(dirname "$0")"

echo "== mesh-policy =="
nickel export check.ncl --format json > /dev/null
echo "ok: talos/mesh-policy.yaml satisfies mesh-policy.ncl"
