// Package boot is the shared start of the actors/cmd binaries: log
// shape, key persistence (-state, -print-id), the iroh bind and the
// actor over it, and actor-id flags. It binds iroh, so everything but
// this file and ids.go sits behind build tag `iroh`, like the binaries
// that use it; it lives under cmd/ because actors/ outside cmd/ stays
// C-free.
package boot
