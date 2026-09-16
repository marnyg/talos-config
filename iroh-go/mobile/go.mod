// P0.2 Android spike core (docs/mesh-v3-p0.2-android.md): a gomobile-bound
// package that runs a gvisor netstack on a VpnService tun fd and turns each
// TCP flow to a fake IP into an iroh stream. Own module so the iroh-go
// smoke build (nix vendorHash) and protocol/ are untouched; the binding
// itself comes from ../ via replace.
module github.com/marnyg/talos-config/iroh-go/mobile

go 1.26.0

require (
	github.com/marnyg/talos-config/iroh-go v0.0.0
	golang.org/x/mobile v0.0.0-20260908204917-8b95e45f8d3e
	golang.org/x/net v0.59.0
	gvisor.dev/gvisor v0.0.0-20250503011706-39ed1f5ac29c
)

require (
	github.com/google/btree v1.1.2 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/time v0.7.0 // indirect
	golang.org/x/tools v0.50.0 // indirect
)

replace github.com/marnyg/talos-config/iroh-go => ../
