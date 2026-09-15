//go:build android

package iroh

// Android is GOOS=android, so the `linux` line in link.go does not apply.
// libiroh_ffi.a built for aarch64-linux-android (Rust std + ring/aws-lc)
// needs bionic's log/dl/m. The -L for the .a is supplied by the package
// that ships it (mobile/link_android.go) or CGO_LDFLAGS.
// #cgo android LDFLAGS: -llog -ldl -lm
import "C"
