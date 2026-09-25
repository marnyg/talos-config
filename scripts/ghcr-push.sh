#!/usr/bin/env bash
# Build a nix2container image (x86_64-linux) and push it to ghcr.io,
# then print it by digest. Shared by k8s/apps/gateway/build.sh and
# actors/build.sh; fly/deploy.sh's shape: the build runs on a linux nix
# store over ssh from a laptop, the push where the closure is, with the
# registry token handed over stdin into a throwaway auth file.
#
#   scripts/ghcr-push.sh <flake attr> <ghcr repo>
#   scripts/ghcr-push.sh .#packages.x86_64-linux.actors-image marnyg/sap-actors
#
#   HUB_BUILDER=mar@nixos   build + push on that box (else: this host)
#   GHCR_TOKEN / GHCR_USER  else the pair docker keeps for ghcr.io
#                           (docker-credential-osxkeychain)
#
# Tag and push derivation come from ONE evaluation and the build is of
# that exact .drv: evaluated separately, an edit made in between named
# a -dirty tag the pushed image did not carry (2026-09-25). The digest
# is read from the registry with the push credentials — `docker
# manifest inspect` prints nothing for a private package.
set -euo pipefail
[ $# -eq 2 ] || { echo "usage: $0 <flake attr> <ghcr repo>" >&2; exit 2; }
attr=$1 repo=$2
cd "$(git rev-parse --show-toplevel)"

store=()
if [ -n "${HUB_BUILDER:-}" ]; then
    store=(--store "ssh-ng://$HUB_BUILDER" --eval-store auto)
fi

eval "$(nix eval --raw "$attr" --apply 'i: "tag=${i.imageTag}; drv=${i.copyToRegistry.drvPath}"')"
image="ghcr.io/$repo:$tag"
echo "building $image${HUB_BUILDER:+ on $HUB_BUILDER}"
push=$(nix build "${store[@]}" --no-link --print-out-paths "$drv^out")

if [ -z "${GHCR_TOKEN:-}" ]; then
    creds=$(printf 'https://ghcr.io' | docker-credential-osxkeychain get)
    user=$(printf '%s' "$creds" | sed -n 's/.*"Username":"\([^"]*\)".*/\1/p')
    GHCR_TOKEN=$(printf '%s' "$creds" | sed -n 's/.*"Secret":"\([^"]*\)".*/\1/p')
else
    user=${GHCR_USER:-marnyg}
fi
auth=$(printf '{"auths":{"ghcr.io":{"auth":"%s"}}}' "$(printf '%s:%s' "$user" "$GHCR_TOKEN" | base64 | tr -d '\n')")
echo "pushing $image"
if [ -n "${HUB_BUILDER:-}" ]; then
    # shellcheck disable=SC2029
    printf '%s' "$auth" | ssh "$HUB_BUILDER" sh -c \
        "'f=\$(mktemp) && trap \"rm -f \$f\" EXIT && cat > \$f && REGISTRY_AUTH_FILE=\$f $push/bin/copy-to-registry'"
else
    f=$(mktemp)
    trap 'rm -f "$f"' EXIT
    printf '%s' "$auth" > "$f"
    REGISTRY_AUTH_FILE="$f" "$push/bin/copy-to-registry"
fi

bearer=$(curl -fsS -u "$user:$GHCR_TOKEN" "https://ghcr.io/token?scope=repository:$repo:pull" |
    sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
digest=$(curl -fsSI -H "Authorization: Bearer $bearer" \
    -H "Accept: application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.v2+json" \
    "https://ghcr.io/v2/$repo/manifests/$tag" | tr -d '\r' | sed -n 's/^docker-content-digest: //Ip')
echo "pushed $image"
echo "image: ghcr.io/$repo@$digest"
