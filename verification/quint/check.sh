#!/usr/bin/env bash
# Check the design models (epic talos-config-7wg; ADR-0015/0017 models
# added 2026-09-05: czi, jp2, 54n). Two tiers:
#
#   ./check.sh          fast: witness tests + random simulation
#   ./check.sh verify   bounded model checking via Apalache (JVM; slower)
#
# quint comes from the flake (`nix develop`); it is not on PATH otherwise.
#
# CI: .github/workflows/verify.yml runs `check.sh run` on every push/PR
# (job `quint`) and `check.sh verify` nightly + on workflow_dispatch (job
# `quint-verify`), both via `nix shell --inputs-from . nixpkgs#quint`, so
# CI and the dev shell use the same flake-pinned quint. Keep this note
# and that workflow in sync.
#
# Models:
#   hub        seal lifecycle; ephemeral state dies with the process
#   enroll     device enrollment (ADR-0012): nonce single-use, addr=f(name)
#   approval   machine approval + ADR-0015 boot-token enrollment; reinstall
#   authorize  ADR-0017/0018 authorize(): the per-connect chain check incl.
#              speak-as resolution (step 2a) over per-process hub keys, and
#              protocol ADR-0001/0002 verifyChain(): the N-link fold it is a
#              case of, verb = the root consent's (invoke | publish);
#              ADR-0003 rule 4: the receiver answers for principals whose
#              speak-as it HOLDS (never the caller's bundle); ADR-0004: the
#              target wildcard `*` never widens, a `*` consent roots nothing;
#              ADR-0007 rev. 2026-09-22 (ydq0): a chain that begins with the
#              rooting consent is folded without it (receiver-side strip)
#   runway     ADR-0017/0018 cert lifetimes vs. sealed-hub starvation incl.
#              the per-process speak-as bound and the 30 d nag
#   clock      ADR-0019 time as trust input: iat low-water mark vs. clock rollback;
#              only receiver-rooted certs feed the mark (strangers never do)
#   rotation   spike aja2 pins 1–4: root rotation via a guardian-signed epoch
#              chain — k-of-n rotate-root certs verified by signature alone,
#              verifier pinned at policy_0 derives its view from the chain
#              rooted at that pin, equivocation at a rooted prev = dead
#
# Expected violation: approval.residualTokenReplayAcrossRedeploy is the
# residual ADR-0015 accepts (a leaked token replays after a redeploy
# inside its TTL). It is checked NEGATIVELY so that a design change
# closing it is noticed and the ADR updated. ADR-0018 (2026-09-06)
# names one such change — token HMAC keyed from the per-process hubkey —
# but leaves it OPEN (it strands tokens served before a redeploy); if
# approval.qnt adopts it, move this to run_inv. rotation has two more:
# rotation.residualTruncatedHistory is the pin-1 residual (a verifier
# never shown the rotation — fresh, starved or fed a truncated history —
# still accepts the stolen old root; a chain read closes it by choice)
# and rotation.residualDeadStaysDead is the pin-2 residual (the
# equivocation record is NOT safe-to-lose: a restart revives the dead
# identity at epoch 0 until git re-feeds the pair). If either holds, the
# design changed — update the pin and move it to run_inv. (The 2026-09-05 findings
# that refuted doc sentences — see FINDING blocks in the headers — were
# ruled the same day and folded into ADR-0015/0017 + the glossary.)
#
# Depths: hub/approval verify at 12–15 steps; enroll's growing sets
# verify at 8; authorize regenerates its whole scenario every step so
# depth 2 is exhaustive (~173 s with the ADR-0004 wildcard target sets,
# zeb; ~170 s since cav.target may name a principal the receiver answers
# for, kau; ~135 s after xwu; ~95 s before); runway's nondet
# init reaches the 6 d boundaries in ≤ 10 steps — the 30 d speak-as
# cliff is 720 h out and is covered by its witness tests (nag*Test,
# rotationConvergesTest) instead; clock's 8-tick horizon is covered at 10;
# rotation's view is a fold over MAX_EPOCH=3 so 7 steps reach every
# chain shape incl. recurrence and equivocation-after-advance (115–140 s;
# 8 steps blew past 5 min). Every invariant
# has been mutation-tested (seeding the bug it guards against produces
# a [violation]); authorize additionally needed a boundary-biased
# generator before its mutants would die.
set -euo pipefail
cd "$(dirname "$0")"

command -v quint >/dev/null || { echo "quint not on PATH — nix develop, or: nix shell --inputs-from . nixpkgs#quint -c $0 $*" >&2; exit 127; }

mode="${1:-run}"

run_inv() { # file invariant steps [samples]
  quint run --invariant="$2" --max-samples="${4:-300}" --max-steps="$3" "$1.qnt"
}

expect_violation() { # file invariant steps [samples]
  if run_inv "$1" "$2" "$3" "${4:-500}" >/dev/null 2>&1; then
    echo "FAIL: $1.$2 was expected to be violated (the doc claim it encodes is now satisfiable — update the FINDING)" >&2
    exit 1
  fi
  echo "ok: $1.$2 violated as documented"
}

case "$mode" in
run)
  for f in approval authorize runway clock rotation; do
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
  echo "== rotation =="
  run_inv rotation invAll 20 500
  echo "== expected violations =="
  expect_violation approval residualTokenReplayAcrossRedeploy 30
  expect_violation rotation residualTruncatedHistory 30 3000   # pin 1 residual: unshown verifier accepts the old root
  expect_violation rotation residualDeadStaysDead 30 3000      # pin 2 residual: restart loses the equivocation record (needs a ≥k thief, a rooted pair, a restart: ~1 trace in 200)
  ;;
verify)
  quint verify --invariant=invAll --max-steps=15 hub.qnt
  quint verify --invariant=invAll --max-steps=12 approval.qnt
  quint verify --invariant=invAll --max-steps=8 enroll.qnt
  quint verify --invariant=invAll --max-steps=2 authorize.qnt
  quint verify --invariant=invAll --max-steps=10 runway.qnt
  quint verify --invariant=invAll --max-steps=10 clock.qnt
  quint verify --invariant=invAll --max-steps=7 rotation.qnt
  ;;
*)
  echo "usage: $0 [run|verify]" >&2
  exit 1
  ;;
esac
