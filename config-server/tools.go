//go:build tools

package main

// Keeps gomobile's bind runtime in go.mod through `go mod tidy`:
// android/build-aar.sh binds ./mobile for the TV/phone app, and
// gomobile refuses to run unless golang.org/x/mobile/bind is a module
// dependency. Never compiled into the server (tools build tag).

import _ "golang.org/x/mobile/bind"
