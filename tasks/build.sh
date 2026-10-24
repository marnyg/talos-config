#!/usr/bin/env bash
# Build the tasks image (tasks/image.nix, x86_64-linux) and push it to
# ghcr.io/marnyg/tasks:<rev> — scripts/ghcr-push.sh does the work.
#
#   HUB_BUILDER=mar@nixos tasks/build.sh   # build + push on the box
#   tasks/build.sh                          # on a linux host
#
# Pin the printed image@digest in k8s/apps/tasks/deployment.yaml.
set -euo pipefail
exec "$(git rev-parse --show-toplevel)/scripts/ghcr-push.sh" .#packages.x86_64-linux.tasks-image marnyg/tasks
