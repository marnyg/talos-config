#!/usr/bin/env bash
# P0.3 build chain (spike; run by hand, each step idempotent):
#
#   1. static agent  — x86_64-linux musl build of iroh-go/cmd/p0agent
#                      (nix build .#p0relay-static on a linux builder)
#   2. extension     — this dir → ghcr.io/marnyg/p0agent:<ver>
#   3. installer     — imager: factory installer 6a9acc… (the three
#                      official extensions the nodes already run) + ours.
#                      The Image Factory takes official extensions only,
#                      so a third-party extension means imager + own
#                      registry; content-addressing is lost (schematic
#                      id → image tag + digest).
#   4. node          — talosctl upgrade --image <installer> (no wipe)
#
# Usage: build.sh <path-to-static-p0agent> [version]
set -euo pipefail
cd "$(dirname "$0")"
bin=${1:?path to static p0agent binary}
ver=${2:-0.0.1}
talos=v1.12.6
base=factory.talos.dev/installer/6a9acceefb4231ee98d04df0a3172479299cf51a36cda05f7ff817ab6d0d4735:$talos
ext=ghcr.io/marnyg/p0agent:$ver
installer=ghcr.io/marnyg/talos-installer:$talos-p0agent-$ver

cp "$bin" ./p0agent
file ./p0agent | grep -q "statically linked" || { echo "not static"; exit 1; }
docker build --platform linux/amd64 -t "$ext" .
docker push "$ext"
rm -f ./p0agent

mkdir -p _out
docker run --rm --platform linux/amd64 -v "$PWD/_out:/out" \
  ghcr.io/siderolabs/imager:$talos installer --arch amd64 \
  --base-installer-image "$base" \
  --system-extension-image "$ext"
docker load < _out/installer-amd64.tar
docker tag ghcr.io/siderolabs/installer-base:$talos "$installer" 2>/dev/null \
  || docker tag "$(docker load < _out/installer-amd64.tar | sed -n 's/^Loaded image: //p')" "$installer"
docker push "$installer"
echo "installer: $installer"
echo "next: talosctl upgrade -n <node> --image $installer"
