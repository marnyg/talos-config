#!/usr/bin/env bash
# Builds app/libs/mobile.aar: the gomobile bind of config-server/mobile
# (keygen, RFC 8628 enrollment, key splice, nebula-on-VpnService-fd).
#
# Requires an Android SDK with an NDK: set ANDROID_HOME (and
# ANDROID_NDK_HOME if the NDK is not under $ANDROID_HOME/ndk).
# CI runs this before gradle; locally it only needs re-running when
# config-server/mobile or its deps change.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
cd "$here/../config-server"

# gomobile + gobind binaries, pinned to the module's x/mobile version
# (tools.go keeps it in go.mod) so the AAR is reproducible from the
# repo alone.
tools="$(mktemp -d)"
trap 'rm -rf "$tools"' EXIT
GOBIN="$tools" go install golang.org/x/mobile/cmd/gomobile golang.org/x/mobile/cmd/gobind
export PATH="$tools:$PATH"

mkdir -p "$here/app/libs"
gomobile bind \
  -target=android \
  -androidapi 26 \
  -o "$here/app/libs/mobile.aar" \
  ./mobile

echo "built $here/app/libs/mobile.aar"
