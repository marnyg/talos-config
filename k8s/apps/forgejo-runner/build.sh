#!/usr/bin/env bash
# Build the CI runner image (k8s/apps/forgejo-runner/image.nix,
# x86_64-linux) and push it to ghcr.io/marnyg/forgejo-runner:<rev> —
# scripts/ghcr-push.sh does the work.
#
#   HUB_BUILDER=mar@nixos k8s/apps/forgejo-runner/build.sh   # build + push on the box
#   k8s/apps/forgejo-runner/build.sh                          # on a linux host
#
# Pin the printed image@digest in deployment.yaml. The package must be
# public for the nodes to pull it (no pull secret). Once the runner is
# up it can rebuild this image itself (nix-cache.yml builds every
# package, this one included) — pushing to ghcr stays a laptop act.
set -euo pipefail
exec "$(git rev-parse --show-toplevel)/scripts/ghcr-push.sh" .#packages.x86_64-linux.forgejo-runner-image marnyg/forgejo-runner
