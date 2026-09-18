#!/usr/bin/env bash
# Deploy the hub: build the nix image (fly/image.nix, x86_64-linux),
# push it to fly's registry, `fly deploy --image`. Replaces `fly deploy`
# + Dockerfile since talos-config-e8d (cgo hub; see fly.toml header).
#
# The image is x86_64-linux only, so from a darwin laptop the build runs
# in a linux nix store over ssh:
#
#   HUB_BUILDER=mar@nixos fly/deploy.sh          # build + push on the box
#   fly/deploy.sh                                # on a linux host (CI)
#   fly/deploy.sh --no-deploy                    # build + push only
#
# The builder needs nothing but nix (the flake is evaluated here and the
# derivations shipped; --eval-store auto). The push runs where the
# image's closure is — on the builder — with the fly token handed over
# stdin into a throwaway auth file, never on a command line. The deploy
# re-seals the hub: sign at /status afterwards (README "Deploying").
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

deploy=1
[ "${1:-}" = "--no-deploy" ] && deploy=0

app=marnyg-talos-config
attr=".#packages.x86_64-linux.hub-image"
store=()
if [ -n "${HUB_BUILDER:-}" ]; then
    store=(--store "ssh-ng://$HUB_BUILDER" --eval-store auto)
fi

echo "building $attr${HUB_BUILDER:+ on $HUB_BUILDER}"
push=$(nix build "${store[@]}" --no-link --print-out-paths "$attr.copyToRegistry")
tag=$(nix eval --raw "$attr.imageTag")
image="registry.fly.io/$app:$tag"

token=$(fly auth token)
auth=$(printf '{"auths":{"registry.fly.io":{"auth":"%s"}}}' "$(printf 'x:%s' "$token" | base64 | tr -d '\n')")
echo "pushing $image"
if [ -n "${HUB_BUILDER:-}" ]; then
    # Explicit sh: the builder's login shell may be fish.
    # shellcheck disable=SC2029
    printf '%s' "$auth" | ssh "$HUB_BUILDER" sh -c \
        "'f=\$(mktemp) && trap \"rm -f \$f\" EXIT && cat > \$f && REGISTRY_AUTH_FILE=\$f $push/bin/copy-to-registry'"
else
    f=$(mktemp)
    trap 'rm -f "$f"' EXIT
    printf '%s' "$auth" > "$f"
    REGISTRY_AUTH_FILE="$f" "$push/bin/copy-to-registry"
fi

if [ "$deploy" = 1 ]; then
    echo "deploying $image"
    fly deploy --app "$app" --image "$image"
    echo "deployed; the hub is SEALED — sign the two proposals at https://$app.fly.dev/status"
else
    echo "pushed $image (not deployed)"
fi
