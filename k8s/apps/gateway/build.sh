#!/usr/bin/env bash
# Build the gateway image (image.nix, x86_64-linux) and push it to
# ghcr.io/marnyg/gateway:<rev> — scripts/ghcr-push.sh does the work.
#
#   HUB_BUILDER=mar@nixos k8s/apps/gateway/build.sh   # build + push on the box
#   k8s/apps/gateway/build.sh                          # on a linux host
#
# Then pin the printed image@digest in deployment.yaml and let ArgoCD
# sync.
set -euo pipefail
exec "$(git rev-parse --show-toplevel)/scripts/ghcr-push.sh" .#packages.x86_64-linux.gateway-image marnyg/gateway
