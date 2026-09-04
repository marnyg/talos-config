#!/usr/bin/env bash
# Check the design models (epic talos-config-7wg; ADR-0015/0017 models
# added 2026-09-05: czi, jp2, 54n). Three tiers:
#
#   ./check.sh          fast: witness tests + random simulation
#   ./check.sh verify   bounded model checking via Apalache (JVM; slower)
#
# Models:
#   hub        seal lifecycle; ephemeral state dies with the process
#   enroll     device enrollment (ADR-0012): nonce single-use, addr=f(name)
#   approval   machine approval + ADR-0015 boot-token enrollment; reinstall
#   authorize  ADR-0017 authorize(): the per-connect chain check
#   runway     ADR-0017 cert lifetimes vs. sealed-hub starvation
#
# Expected violations: some models carry an invariant that encodes a
# design claim AS WRITTEN in the docs, which the model refutes (see the
# FINDING block in each header). Those are checked negatively below so
# that a later spec change flips them visibly:
#   approval.invTokenSingleUse   ADR-0015 "single-use" token vs. stateless verify
#   runway.invClaimAsWritten     ADR-0017 "sealed < 7 d loses no access"
#
# Depths: hub/approval verify at 12–15 steps; enroll's growing sets
# verify at 8; authorize regenerates its whole scenario every step so
# depth 2 is exhaustive; runway's nondet init reaches boundaries in
# ≤ 10 steps. Every invariant has been mutation-tested (seeding the bug
# it guards against produces a [violation]); authorize additionally
# needed a boundary-biased generator before its mutants would die.
set -euo pipefail
cd "$(dirname "$0")"

mode="${1:-run}"

run_inv() { # file invariant steps [samples]
  quint run --invariant="$2" --max-samples="${4:-300}" --max-steps="$3" "$1.qnt"
}

expect_violation() { # file invariant steps
  if quint run --invariant="$2" --max-samples=500 --max-steps="$3" "$1.qnt" >/dev/null 2>&1; then
    echo "FAIL: $1.$2 was expected to be violated (the doc claim it encodes is now satisfiable — update the FINDING)" >&2
    exit 1
  fi
  echo "ok: $1.$2 violated as documented"
}

case "$mode" in
run)
  for f in approval authorize runway; do
    echo "== $f (tests) =="
    quint test "$f.qnt"
  done
  for f in hub enroll approval; do
    echo "== $f =="
    run_inv "$f" invAll 30
  done
  echo "== authorize =="
  run_inv authorize invAll 20 1000
  echo "== runway =="
  run_inv runway invAll 60 1000
  echo "== expected violations =="
  expect_violation approval invTokenSingleUse 30
  expect_violation runway invClaimAsWritten 20
  ;;
verify)
  quint verify --invariant=invAll --max-steps=15 hub.qnt
  quint verify --invariant=invAll --max-steps=12 approval.qnt
  quint verify --invariant=invAll --max-steps=8 enroll.qnt
  quint verify --invariant=invAll --max-steps=2 authorize.qnt
  quint verify --invariant=invAll --max-steps=10 runway.qnt
  ;;
*)
  echo "usage: $0 [run|verify]" >&2
  exit 1
  ;;
esac
