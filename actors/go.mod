module github.com/marnyg/talos-config/actors

go 1.26.0

require github.com/marnyg/talos-config/protocol v0.0.0

require (
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.4.1 // indirect
	github.com/gowebpki/jcs v1.0.1 // indirect
	github.com/marnyg/talos-config/iroh-go v0.0.0 // indirect
	github.com/marnyg/talos-config/iroh-transport v0.0.0
	golang.org/x/crypto v0.56.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/marnyg/talos-config/protocol => ../protocol

replace github.com/marnyg/talos-config/iroh-transport => ../iroh-transport

replace github.com/marnyg/talos-config/iroh-go => ../iroh-go
