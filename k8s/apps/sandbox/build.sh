#!/usr/bin/env bash
# Build the sandbox agent image (k8s/apps/sandbox/image.nix, x86_64-linux)
# and push it to ghcr.io/marnyg/sandbox-agent:<rev> — scripts/ghcr-push.sh
# does the work.
#
#   HUB_BUILDER=mar@nixos k8s/apps/sandbox/build.sh   # build + push on the box
#   k8s/apps/sandbox/build.sh                          # on a linux host
#
# The digest is pinned by hand in THREE places, and the k8s driver keys
# the outfit on the exact image string — so the sap-provisioner -outfit
# key and the sap-parent -image must be byte-identical, or the child
# boots keyless. After the push this prints the three lines to paste,
# one per file. The package must be public for the nodes to pull it
# (no pull secret in the Job).
set -euo pipefail
repo=marnyg/sandbox-agent
out=$("$(git rev-parse --show-toplevel)/scripts/ghcr-push.sh" .#packages.x86_64-linux.sandbox-agent-image "$repo" | tee /dev/stderr)

# ghcr-push.sh ends with `pushed ghcr.io/<repo>:<tag>` and
# `image: ghcr.io/<repo>@sha256:…`; parse the tag and digest from those.
tag=$(sed -n "s|^pushed ghcr.io/$repo:||p" <<<"$out" | tail -n1)
digest=$(sed -n "s|^image: ghcr.io/$repo@||p" <<<"$out" | tail -n1)
[ -n "$tag" ] && [ -n "$digest" ] || { echo "could not parse tag/digest from ghcr-push.sh output" >&2; exit 1; }

cat <<EOF

pin these three lines (all three, same digest):
  k8s/apps/sandbox/agent.yaml                 image: ghcr.io/$repo:$tag@$digest
  k8s/apps/sap-provisioner/deployment.yaml    - -outfit=ghcr.io/$repo@$digest=openrouter,forgejo-agent
  k8s/apps/sap-parent/deployment.yaml         - -image=ghcr.io/$repo@$digest
EOF
