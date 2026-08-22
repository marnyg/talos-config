#!/usr/bin/env bash
# Check the design models (epic talos-config-7wg). Two tiers:
#
#   ./check.sh          fast: random simulation, 300 traces x 30 steps
#   ./check.sh verify   bounded model checking via Apalache (JVM; slower)
#
# Depths: hub and approval verify at 15 steps in seconds. enroll's
# state space (growing cert/nonce sets) makes deep verification
# expensive, so it verifies at 8 steps; the simulation tier covers
# longer traces. Every invariant has been mutation-tested: seeding the
# bug it guards against (overlay surviving deploy, replayable
# challenge, certs wiped on redeploy, reusable approval grant) produces
# a [violation].
set -euo pipefail
cd "$(dirname "$0")"

mode="${1:-run}"

case "$mode" in
run)
  for f in hub enroll approval; do
    echo "== $f =="
    quint run --invariant=invAll --max-samples=300 --max-steps=30 "$f.qnt"
  done
  ;;
verify)
  quint verify --invariant=invAll --max-steps=15 hub.qnt
  quint verify --invariant=invAll --max-steps=15 approval.qnt
  quint verify --invariant=invAll --max-steps=8 enroll.qnt
  ;;
*)
  echo "usage: $0 [run|verify]" >&2
  exit 1
  ;;
esac
