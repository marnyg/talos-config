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
#   clock      ADR-0019 time as trust input: iat low-water mark vs. clock rollback
#
# Expected violation: approval.residualTokenReplayAcrossRedeploy is the
# residual ADR-0015 accepts (a leaked token replays after a redeploy
# inside its TTL). It is checked NEGATIVELY so that a design change
# closing it is noticed and the ADR updated. ADR-0018 (2026-09-06)
# names one such change — token HMAC keyed from the per-process hubkey —
# but leaves it OPEN (it strands tokens served before a redeploy); if
# approval.qnt adopts it, move this to run_inv. (The 2026-09-05 findings
# that refuted doc sentences — see FINDING blocks in the headers — were
# ruled the same day and folded into ADR-0015/0017 + the glossary.)
#
# Depths: hub/approval verify at 12–15 steps; enroll's growing sets
# verify at 8; authorize regenerates its whole scenario every step so
# depth 2 is exhaustive; runway's nondet init reaches boundaries in
# ≤ 10 steps; clock's 8-tick horizon is covered at 10. Every invariant
# has been mutation-tested (seeding the bug it guards against produces
# a [violation]); authorize additionally needed a boundary-biased
# generator before its mutants would die.
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
  for f in approval authorize runway clock; do
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
  echo "== clock =="
  run_inv clock invAll 30 500
  echo "== expected violations =="
  expect_violation approval residualTokenReplayAcrossRedeploy 30
  ;;
verify)
  quint verify --invariant=invAll --max-steps=15 hub.qnt
  quint verify --invariant=invAll --max-steps=12 approval.qnt
  quint verify --invariant=invAll --max-steps=8 enroll.qnt
  quint verify --invariant=invAll --max-steps=2 authorize.qnt
  quint verify --invariant=invAll --max-steps=10 runway.qnt
  quint verify --invariant=invAll --max-steps=10 clock.qnt
  ;;
*)
  echo "usage: $0 [run|verify]" >&2
  exit 1
  ;;
esac
