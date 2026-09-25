#!/usr/bin/env bash
# Build the actors image (actors/image.nix, x86_64-linux) and push it to
# ghcr.io/marnyg/sap-actors:<rev>. fly/deploy.sh's shape: the build runs
# on a linux nix store over ssh from a laptop, the push where the
# closure is, with the registry token handed over stdin into a
# throwaway auth file.
#
#   HUB_BUILDER=mar@nixos actors/build.sh   # build + push on the box
#   actors/build.sh                          # on a linux host
#
# Token: GHCR_TOKEN in the environment, else the one docker keeps for
# ghcr.io (docker-credential-osxkeychain). Then pin the printed
# image@digest: the parent names it in #spawn (spawn.Spec.Image).
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

attr=".#packages.x86_64-linux.actors-image"
store=()
if [ -n "${HUB_BUILDER:-}" ]; then
    store=(--store "ssh-ng://$HUB_BUILDER" --eval-store auto)
fi

echo "building $attr${HUB_BUILDER:+ on $HUB_BUILDER}"
push=$(nix build "${store[@]}" --no-link --print-out-paths "$attr.copyToRegistry")
tag=$(nix eval --raw "$attr.imageTag")
image="ghcr.io/marnyg/sap-actors:$tag"

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
echo "pushed $image"
echo "digest: $(docker manifest inspect -v "$image" 2>/dev/null | sed -n 's/.*"digest": "\(sha256:[a-f0-9]*\)".*/\1/p' | head -1 || true)"
