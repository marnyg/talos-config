#!/usr/bin/env bash
# Builds app/libs/p0mobile.aar: gomobile bind of ../mobile (package p0mobile)
# for android/arm64 only, linking the Android build of libiroh_ffi.a.
#
# Inputs (env):
#   ANDROID_HOME, ANDROID_NDK_HOME   Android SDK + NDK (gomobile needs both)
#   IROH_FFI_ANDROID_LIB             dir containing libiroh_ffi.a built for
#                                    aarch64-linux-android (nix: see
#                                    docs/mesh-v3-p0.2-android.md step 1)
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
: "${IROH_FFI_ANDROID_LIB:?dir with the aarch64-linux-android libiroh_ffi.a}"
[ -f "$IROH_FFI_ANDROID_LIB/libiroh_ffi.a" ] || { echo "no libiroh_ffi.a in $IROH_FFI_ANDROID_LIB" >&2; exit 1; }

cd "$here/../mobile"
tools="$(mktemp -d)"
trap 'rm -rf "$tools"' EXIT
GOBIN="$tools" go install golang.org/x/mobile/cmd/gomobile golang.org/x/mobile/cmd/gobind
export PATH="$tools:$PATH"

mkdir -p "$here/app/libs"
# -L for the .a: the iroh package's link_android.go adds -liroh_ffi -llog -ldl -lm.
CGO_LDFLAGS="-L$IROH_FFI_ANDROID_LIB" gomobile bind \
  -target=android/arm64 \
  -androidapi 26 \
  -o "$here/app/libs/p0mobile.aar" \
  .
echo "built $here/app/libs/p0mobile.aar"
