#!/usr/bin/env bash
# One-shot APK build on the NixOS builder (mar@nixos): the three hand-run
# steps from README.md "Building" — libiroh_ffi.a cross build (cached
# after the first ~1 h), build-aar.sh, gradle assembleDebug — plus the
# two steps nobody remembered (talos-config-4zpf): publish to the rolling
# `android-latest` release and install on a device.
#
#   android/build.sh                 build only
#   android/build.sh --publish       + publish.sh (gh authenticated)
#   android/build.sh --install SER   + adb -s SER install -r (repeatable;
#                                      SER = serial or host:port)
#
# Writes app-debug.apk.sha next to the APK: the commit the APK was
# built from (+ "-dirty" on uncommitted changes). publish.sh puts that
# in the release notes instead of the publisher's HEAD, so the asset
# says what it is even when published days later.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
cd "$here"

publish=0
installs=()
while [ $# -gt 0 ]; do
  case "$1" in
    --publish) publish=1 ;;
    --install) installs+=("${2:?--install needs a serial}"); shift ;;
    -h|--help) sed -n '2,/^set -/p' "$0" | sed '$d; s/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
  shift
done

export NIXPKGS_ALLOW_UNFREE=1
sha=$(git rev-parse --short HEAD)
git diff --quiet HEAD -- . ../config-server ../iroh-go ../protocol ../iroh-transport 2>/dev/null || sha="$sha-dirty"
echo "== building @ $sha"

echo "== libiroh_ffi.a for aarch64-linux-android (iroh-go/nix/android.nix)"
lib=$(nix build --impure -f ../iroh-go/nix/android.nix --print-out-paths)/lib
echo "   $lib"

echo "== build-aar.sh → app/libs/mobile.aar"
nix-shell --impure shell.nix --run "IROH_FFI_ANDROID_LIB=$lib ./build-aar.sh"

echo "== gradle assembleDebug"
nix-shell --impure shell.nix --run "gradle --no-daemon assembleDebug"

apk="$here/app/build/outputs/apk/debug/app-debug.apk"
echo "$sha" > "$apk.sha"
ls -la "$apk"
echo "== built $apk @ $sha"

if [ "$publish" = 1 ]; then
  echo "== publish.sh"
  ./publish.sh "$apk"
fi

# adb comes from the SDK in shell.nix (the builder has none on PATH).
for ser in "${installs[@]}"; do
  echo "== adb -s $ser install -r"
  nix-shell --impure shell.nix --run "adb -s '$ser' install -r '$apk'"
done
