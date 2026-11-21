#!/usr/bin/env bash
# Publishes the debug-signed APK to the rolling `android-latest` release
# — what .github/workflows/android-apk.yml did on every push until P2.4
# moved the AAR build off CI (see the workflow header). Run from the
# builder after `gradle assembleDebug` (or `build.sh --publish`), with
# `gh` authenticated.
#
#   android/publish.sh [path-to-apk]
#
# The release notes name the commit the APK was built from: build.sh
# leaves it in <apk>.sha. Without that sidecar (a bare gradle run) the
# publisher's HEAD is the best guess and the notes say so:
# "<sha> (unverified: publisher HEAD)".
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
apk=${1:-$here/app/build/outputs/apk/debug/app-debug.apk}
[ -f "$apk" ] || { echo "no APK at $apk" >&2; exit 1; }
if [ -f "$apk.sha" ]; then
  sha=$(cat "$apk.sha")
else
  sha="$(git -C "$here" rev-parse --short HEAD) (unverified: publisher HEAD)"
  echo "warning: $apk.sha missing — notes will carry HEAD, not the APK's build sha" >&2
fi
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
cp "$apk" "$tmp/talos-mesh.apk"

# The update manifest: what the installed app compares itself with
# (app/.../Updater.kt). versionCode/versionName come from the metadata
# gradle leaves beside the APK, so they are the APK's own. An APK with
# no metadata is published without touching the manifest — devices then
# simply are not told about it.
meta="$(dirname "$apk")/output-metadata.json"
code=$(sed -n 's/.*"versionCode": *\([0-9][0-9]*\).*/\1/p' "$meta" 2>/dev/null | head -1)
name=$(sed -n 's/.*"versionName": *"\([^"]*\)".*/\1/p' "$meta" 2>/dev/null | head -1)
if [ -n "$code" ]; then
  # Android refuses to install a lower versionCode over a higher one, so
  # publishing one (a shallow clone, an old branch) strands every device
  # that follows the prompt. FORCE=1 to do it anyway.
  was=$(gh release download android-latest -p talos-mesh.json -O - 2>/dev/null \
    | sed -n 's/.*"versionCode": *\([0-9][0-9]*\).*/\1/p' | head -1) || true # none published yet
  if [ -n "$was" ] && [ "$code" -lt "$was" ] && [ "${FORCE:-0}" != 1 ]; then
    echo "refusing: this APK is versionCode $code, the published one is $was (FORCE=1 overrides)" >&2
    exit 1
  fi
  printf '{"versionCode": %s, "versionName": "%s", "sha": "%s"}\n' "$code" "${name:-$code}" "$sha" > "$tmp/talos-mesh.json"
else
  echo "warning: no versionCode in $meta — the update manifest is left as it is" >&2
fi
gh release view android-latest > /dev/null 2>&1 || \
  gh release create android-latest --prerelease \
    --title "Android app (rolling)" \
    --notes "Debug-signed APK. Sideload: download this asset on the TV and install."
gh release upload android-latest "$tmp/talos-mesh.apk" --clobber
# After the APK, so the manifest never announces a build that is not there.
[ -f "$tmp/talos-mesh.json" ] && gh release upload android-latest "$tmp/talos-mesh.json" --clobber
size=$(stat -c %s "$apk" 2>/dev/null || stat -f %z "$apk")
gh release edit android-latest --notes "Debug-signed APK built by hand on the NixOS builder from talos-config ${sha} ($(( size / 1024 / 1024 )) MB, $(date -u +%Y-%m-%d)). Sideload: download this asset on the TV and install, or \`android/build.sh --install <serial>\`."
echo "published talos-mesh.apk @ $sha${code:+ (versionCode $code)}"
