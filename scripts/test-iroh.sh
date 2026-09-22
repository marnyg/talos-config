#!/usr/bin/env bash
# Run the cgo/iroh half of the Go test suites — the part `go test ./...`
# silently skips.
#
# Why this exists (talos-config-ydq0, 2026-09-22): the iroh binding is
# behind build tag `iroh` (config-server/hubiroh.go, stub in
# hubiroh_stub.go), so a bare `go test ./...` under config-server/ is
# C-free and green while the tagged suite is red. The only thing that
# ran the tagged suite was `nix build .#config-server-bin` — i.e. a hub
# deploy. A protocol change (M3, 5c2bf7d) broke the node beat and main
# stayed undeployable for three commits; the deploy found out, mid
# node-provisioning, with the hub already sealed.
#
# So: run this before pushing anything under protocol/, config-server/
# or iroh-transport/. It is the same thing the nix build does, minus the
# sandbox, and it reuses the dev machine's Go cache (~40 s warm vs. the
# nix build's few minutes).
#
#   scripts/test-iroh.sh                  both modules, all packages
#   scripts/test-iroh.sh config-server    one module
#   scripts/test-iroh.sh config-server -run TestHubBeatOverIroh
#
# Anything after the module name is passed through to `go test`.
#
# NOTE: this is not a substitute for the pre-push vendorHash check (that
# is .githooks/pre-push) — different failure, same trees.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

mod="${1:-}"
[ $# -gt 0 ] && shift

case "$mod" in
    "" | all) mods="config-server iroh-transport" ;;
    config-server | iroh-transport) mods="$mod" ;;
    *)
        echo "usage: $0 [config-server|iroh-transport|all] [go test args...]" >&2
        exit 2
        ;;
esac

# Built by the flake; not on PATH otherwise. --print-out-paths so a cold
# cache builds them rather than failing obscurely inside go test.
echo "resolving iroh-ffi-static and iroh-relay…" >&2
lib=$(nix build .#iroh-ffi-static --no-link --print-out-paths)/lib
relay=$(nix build .#iroh-relay --no-link --print-out-paths)/bin/iroh-relay

export CGO_ENABLED=1
export CGO_LDFLAGS="-L$lib"
export IROH_RELAY_BIN="$relay"

rc=0
for m in $mods; do
    echo "== $m (-tags iroh) ==" >&2
    (cd "$m" && go test -tags iroh -count=1 "$@" ./...) || rc=1
done

if [ "$rc" != 0 ]; then
    echo "FAIL: the tagged suite is red — do NOT push; the hub image build will fail the same way" >&2
fi
exit "$rc"
