#!/usr/bin/env bash
# Build the vmpower image (vmpower/image.nix, x86_64-linux) and push it
# to ghcr.io/marnyg/vmpower:<rev> — scripts/ghcr-push.sh does the work.
#
#   HUB_BUILDER=mar@nixos vmpower/build.sh   # build + push on the box
#   vmpower/build.sh                          # on a linux host
#
# Pin the printed image@digest in k8s/apps/vmpower/deployment.yaml.
set -euo pipefail
exec "$(git rev-parse --show-toplevel)/scripts/ghcr-push.sh" .#packages.x86_64-linux.vmpower-image marnyg/vmpower
