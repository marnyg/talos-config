//go:build tools

package p0mobile

// Keeps gomobile's bind runtime (and its cmd/ deps) in go.mod through
// `go mod tidy`: android-p0/build-aar.sh binds this package and gomobile
// refuses to run unless golang.org/x/mobile/bind is a module dependency.
// Same pattern as config-server/tools.go. Never compiled (tools tag).

import (
	_ "golang.org/x/mobile/bind"
	_ "golang.org/x/mobile/cmd/gobind"
	_ "golang.org/x/mobile/cmd/gomobile"
)
