#!/usr/bin/env bash
# Builds app/libs/mobile.aar: the gomobile bind of config-server/mobile
# (the member on a VpnService: nodeagent + meshtun over iroh, P2.4) for
# android/arm64, linking the Android build of libiroh_ffi.a statically.
#
# Inputs (env):
#   ANDROID_HOME, ANDROID_NDK_HOME   Android SDK + NDK (gomobile needs both)
#   IROH_FFI_ANDROID_LIB             dir containing libiroh_ffi.a built for
#                                    aarch64-linux-android:
#                                      NIXPKGS_ALLOW_UNFREE=1 nix build --impure \
#                                        -f iroh-go/nix/android.nix --print-out-paths
#                                    (+ /lib). ~1 h cold on a linux builder,
#                                    cached in the store after; see
#                                    android/shell.nix for the whole shell.
#
# CI does not run this yet: the cross build is impure (NDK, unfree) and
# an hour cold. Build on the NixOS box (mar@nixos), commit nothing — the
# AAR is an artifact, the APK the release.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
: "${IROH_FFI_ANDROID_LIB:?dir with the aarch64-linux-android libiroh_ffi.a}"
[ -f "$IROH_FFI_ANDROID_LIB/libiroh_ffi.a" ] || { echo "no libiroh_ffi.a in $IROH_FFI_ANDROID_LIB" >&2; exit 1; }
cd "$here/../config-server"

# gomobile + gobind binaries, pinned to the module's x/mobile version
# (tools.go keeps it in go.mod) so the AAR is reproducible from the
# repo alone.
tools="$(mktemp -d)"
trap 'rm -rf "$tools"' EXIT
GOBIN="$tools" go install golang.org/x/mobile/cmd/gomobile golang.org/x/mobile/cmd/gobind
export PATH="$tools:$PATH"

# Stage only the .a: the nix output ships libiroh_ffi.so next to it and
# lld picks the shared one for -liroh_ffi, which would leave libgojni.so
# with a NEEDED on a library the APK does not carry. iroh-go/iroh/link.go
# adds -liroh_ffi, link_android.go adds -llog -ldl -lm.
staticdir="$tools/iroh-static"
mkdir -p "$staticdir"
ln -s "$IROH_FFI_ANDROID_LIB/libiroh_ffi.a" "$staticdir/libiroh_ffi.a"

mkdir -p "$here/app/libs"
# max-page-size=16384: 16 KB-aligned LOAD segments so the .so is loadable
# on 16 KB page devices (Android 15+) however the APK packages it.
CGO_LDFLAGS="-L$staticdir" gomobile bind \
  -target=android/arm64 \
  -androidapi 26 \
  -tags iroh \
  -ldflags='-extldflags=-Wl,-z,max-page-size=16384' \
  -o "$here/app/libs/mobile.aar" \
  ./mobile

echo "built $here/app/libs/mobile.aar"
