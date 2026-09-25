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

# The tag first: evaluated after the build, an edit made meanwhile
# names a -dirty tag the pushed image does not carry.
tag=$(nix eval --raw "$attr.imageTag")
image="ghcr.io/marnyg/sap-actors:$tag"
echo "building $attr${HUB_BUILDER:+ on $HUB_BUILDER}"
push=$(nix build "${store[@]}" --no-link --print-out-paths "$attr.copyToRegistry")

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
# The digest from the registry itself, with the same credentials (the
# package may be private; docker manifest inspect then prints nothing).
bearer=$(curl -fsS -u "$user:$GHCR_TOKEN" "https://ghcr.io/token?scope=repository:marnyg/sap-actors:pull" |
    sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
digest=$(curl -fsSI -H "Authorization: Bearer $bearer" \
    -H "Accept: application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.v2+json" \
    "https://ghcr.io/v2/marnyg/sap-actors/manifests/$tag" | tr -d '\r' | sed -n 's/^docker-content-digest: //Ip')
echo "image: ghcr.io/marnyg/sap-actors@$digest"
