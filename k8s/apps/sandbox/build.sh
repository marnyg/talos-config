#!/usr/bin/env bash
# Build the sandbox agent image (k8s/apps/sandbox/image.nix, x86_64-linux)
# and push it to ghcr.io/marnyg/sandbox-agent:<rev> — scripts/ghcr-push.sh
# does the work.
#
#   HUB_BUILDER=mar@nixos k8s/apps/sandbox/build.sh   # build + push on the box
#   k8s/apps/sandbox/build.sh                          # on a linux host
#
# Pin the printed image@digest in agent.yaml. The package must be public
# for the nodes to pull it (no pull secret in the Job).
set -euo pipefail
exec "$(git rev-parse --show-toplevel)/scripts/ghcr-push.sh" .#packages.x86_64-linux.sandbox-agent-image marnyg/sandbox-agent
