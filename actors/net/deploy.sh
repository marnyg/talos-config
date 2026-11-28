#!/usr/bin/env bash
# Deploy the protocol network's fly app (actors/net/fly.toml): build the
# nix image (actors/net/image.nix, x86_64-linux), push it to fly's
# registry, `fly deploy --image`. fly/deploy.sh's shape, for the other
# app.
#
#   HUB_BUILDER=mar@nixos actors/net/deploy.sh   # build + push on the box
#   actors/net/deploy.sh                         # on a linux host
#   actors/net/deploy.sh --no-deploy             # build + push only
#   NET_CONFIG=/tmp/fly.toml actors/net/deploy.sh   # another fly.toml
#                                                # (a one-off member)
#
# Nothing to sign afterwards: the lighthouse holds its own key on the
# app's volume. A deploy restarts the relay — every actor reconnects,
# members at once, children on their next beat.
#
# The app and its volume are created by hand, once (fly.toml's header).
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

deploy=1
[ "${1:-}" = "--no-deploy" ] && deploy=0

app=marnyg-sap-net
config=${NET_CONFIG:-actors/net/fly.toml}
attr=".#packages.x86_64-linux.net-image"
store=()
if [ -n "${HUB_BUILDER:-}" ]; then
    store=(--store "ssh-ng://$HUB_BUILDER" --eval-store auto)
fi

# Tag and push derivation from ONE evaluation (fly/deploy.sh's note).
eval "$(nix eval --raw "$attr" --apply 'i: "tag=${i.imageTag}; drv=${i.copyToRegistry.drvPath}"')"
image="registry.fly.io/$app:$tag"
echo "building $image${HUB_BUILDER:+ on $HUB_BUILDER}"
push=$(nix build "${store[@]}" --no-link --print-out-paths "$drv^out")

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
    echo "deploying $image ($config)"
    # One machine: the key's volume is one volume.
    fly deploy --app "$app" --config "$config" --image "$image" --ha=false
    echo "deployed; https://$app.fly.dev/generate_204 answers 204 when the relay is up behind its gate"
else
    echo "pushed $image (not deployed)"
fi
