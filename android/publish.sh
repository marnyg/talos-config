#!/usr/bin/env bash
# Publishes the debug-signed APK to the rolling `android-latest` release
# — what .github/workflows/android-apk.yml did on every push until P2.4
# moved the AAR build off CI (see the workflow header). Run from the
# builder after `gradle assembleDebug`, with `gh` authenticated.
#
#   android/publish.sh [path-to-apk]
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
apk=${1:-$here/app/build/outputs/apk/debug/app-debug.apk}
[ -f "$apk" ] || { echo "no APK at $apk" >&2; exit 1; }
sha=$(git -C "$here" rev-parse --short HEAD)
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
cp "$apk" "$tmp/talos-mesh.apk"
gh release view android-latest > /dev/null 2>&1 || \
  gh release create android-latest --prerelease \
    --title "Android app (rolling)" \
    --notes "Debug-signed APK. Sideload: download this asset on the TV and install."
gh release upload android-latest "$tmp/talos-mesh.apk" --clobber
gh release edit android-latest --notes "Debug-signed APK, built by hand on the NixOS builder (last: ${sha}). Sideload: download this asset on the TV and install."
echo "published talos-mesh.apk @ $sha"
