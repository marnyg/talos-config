#!/usr/bin/env bash
# Build the actors image (actors/image.nix, x86_64-linux) and push it to
# ghcr.io/marnyg/sap-actors:<rev> — scripts/ghcr-push.sh does the work.
#
#   HUB_BUILDER=mar@nixos actors/build.sh   # build + push on the box
#   actors/build.sh                          # on a linux host
#
# Pin the printed image@digest: the parent names it in #spawn
# (spawn.Spec.Image). The package must be public for the drivers to
# pull it (registry auth is not v0).
set -euo pipefail
exec "$(git rev-parse --show-toplevel)/scripts/ghcr-push.sh" .#packages.x86_64-linux.actors-image marnyg/sap-actors
