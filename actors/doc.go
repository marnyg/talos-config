// Package actors is the runnable side of the sovereign-actor protocol:
// the platform drivers a provisioner renders leases through
// (driver/k8s, driver/docker) and, later, the binaries
// (cmd/provisioner, cmd/child). Its own Go module, as iroh-transport/
// is, so protocol/ never imports a platform SDK (ADR-0009) and the
// drivers' tests stay C-free — the iroh binding enters only in cmd/.
package actors
