#!/usr/bin/env bash
# Build the workouts image (workouts/image.nix, x86_64-linux) and push it
# to ghcr.io/marnyg/workouts:<rev> — scripts/ghcr-push.sh does the work.
#
#   HUB_BUILDER=mar@nixos workouts/build.sh   # build + push on the box
#   workouts/build.sh                          # on a linux host
#
# Pin the printed image@digest in k8s/apps/workouts/deployment.yaml.
set -euo pipefail
exec "$(git rev-parse --show-toplevel)/scripts/ghcr-push.sh" .#packages.x86_64-linux.workouts-image marnyg/workouts
