// Package actors is the runnable side of the sovereign-actor protocol:
// the platform drivers a provisioner renders leases through
// (driver/k8s, driver/docker), the child's life on the beat (child),
// and the two binaries (cmd/provisioner, cmd/child). Its own Go
// module, as iroh-transport/ is, so protocol/ never imports a platform
// SDK (ADR-0009). Everything but cmd/ is C-free and tests untagged;
// cmd/ binds iroh behind build tag `iroh`, compiled and tested only by
// `nix build .#actors-bin` (AGENTS.md's quality-gate caveat applies).
package actors
