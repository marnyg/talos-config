#!/usr/bin/env bash
# Build chain for cp1's declared install image (talos/hardware/minipc.yaml).
# Born as the Mesh v3 P0.3 spike; adopted at the Phase 0 gate 2026-09-16
# (bead 5cz). Run by hand, each step idempotent; after a push, update the
# tag AND the digest in minipc.yaml (`crane digest` or the registry's
# Docker-Content-Digest header):
#
#   1. static agent  — x86_64-linux musl build of iroh-go/cmd/p0agent
#                      (nix build .#p0relay-static on a linux builder)
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
# Usage: build.sh <path-to-static-p0agent> [version]
set -euo pipefail
cd "$(dirname "$0")"
bin=${1:?path to static p0agent binary}
ver=${2:-0.0.1}
talos=v1.12.6
# refs from https://factory.talos.dev/version/$talos/extensions/official
official=(
  ghcr.io/siderolabs/iscsi-tools:v0.2.0
  ghcr.io/siderolabs/nebula:1.10.3
  ghcr.io/siderolabs/util-linux-tools:2.41.2
)
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
  "${official[@]/#/--system-extension-image=}" \
  --system-extension-image "$ext"
# imager tags the tarball with the BASE image's name — retag, then drop the
# misleading local factory tag.
loaded=$(docker load < _out/installer-amd64.tar | sed -n 's/^Loaded image: //p')
docker tag "$loaded" "$installer"
docker rmi "$loaded" >/dev/null
docker push "$installer"
echo "installer: $installer"
echo "next: talosctl upgrade -n <node> --image $installer"
