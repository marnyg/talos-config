#!/usr/bin/env bash
# Build chain for the fleet's declared install image (talos/hardware/
# minipc.yaml + alienware-x15.yaml). Born as the Mesh v3 P0.3 spike;
# adopted at the Phase 0 gate 2026-09-16 (bead 5cz). Run by hand, each
# step idempotent; the last line prints the tag@digest to pin in BOTH
# hardware files. Talos version + official extensions: ../installer.env.
#
#   1. static agent  — x86_64-linux musl build of config-server/cmd/nodeagent:
#                      `nix build .#config-server-static` on a linux builder
#                      (mar@nixos) yields result/bin/nodeagent beside the hub
#                      binary. (Was iroh-go/cmd/p0agent via .#p0relay-static
#                      for the P0.3 probe.)
#   2. extension     — this dir → ghcr.io/marnyg/p0agent:<ver>
#   3. installer     — imager: stock installer + the three official
#                      extensions the nodes already run (schematic 6a9acc…)
#                      + ours. The Image Factory takes official extensions
#                      only, so a third-party extension means imager + own
#                      registry; content-addressing is lost (schematic id →
#                      image tag + digest). NOTE --base-installer-image with
#                      the factory installer does NOT inherit its
#                      extensions (learned 2026-09-15: cp1 came up with
#                      p0agent only) — list every extension explicitly.
#   4. node          — talosctl upgrade --image <installer> (no wipe)
#
# Usage: build.sh <path-to-static-nodeagent> [version]
set -euo pipefail
cd "$(dirname "$0")"
bin=${1:?path to static nodeagent binary}
ver=${2:-0.1.0}
# Talos version + official extension refs live in ../installer.env so
# the hardware files and this script cannot drift apart.
# shellcheck source=../installer.env
. ../installer.env
talos=$TALOS
official=("${OFFICIAL[@]}")
ext=ghcr.io/marnyg/p0agent:$ver
installer=ghcr.io/marnyg/talos-installer:$talos-p0agent-$ver

# manifest.yaml is what `talosctl get extensions` reports: stamp it from
# the version we tag with, and commit the result (0.1.2 shipped with the
# manifest still saying 0.1.1 — the nodes report that until 0.1.3).
sed -i.bak "s/^  version: .*/  version: $ver/" manifest.yaml && rm -f manifest.yaml.bak
grep -q "^  version: $ver\$" manifest.yaml || { echo "manifest.yaml: version not stamped"; exit 1; }

cp "$bin" ./nodeagent
file ./nodeagent | grep -q "statically linked" || { echo "not static"; exit 1; }
docker build --platform linux/amd64 -t "$ext" .
docker push "$ext"
rm -f ./nodeagent

mkdir -p _out
docker run --rm --platform linux/amd64 -v "$PWD/_out:/out" \
  ghcr.io/siderolabs/imager:$talos installer --arch amd64 \
  "${official[@]/#/--system-extension-image=}" \
  --system-extension-image "$ext"
# imager tags the tarball with the BASE image's name — retag, then drop the
# misleading local factory tag.
loaded=$(docker load < _out/installer-amd64.tar | sed -n 's/^Loaded image: //p')
docker tag "$loaded" "$installer"
docker rmi "$loaded" >/dev/null
docker push "$installer"
echo "installer: $installer"
digest=$(docker buildx imagetools inspect "$installer" --format '{{json .Manifest.Digest}}' | tr -d '"')
echo "pin in talos/hardware/*.yaml: image: $installer@$digest"
echo "next: talosctl upgrade -n <node> --image $installer@$digest"
