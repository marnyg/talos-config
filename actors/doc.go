// Package actors is the runnable side of the sovereign-actor protocol:
// the platform drivers a provisioner renders leases through
// (driver/k8s, driver/docker), the child's life on the beat (child),
// the admission gate in front of the network's relay (relaygate,
// protocol ADR-0012), and the binaries (cmd/provisioner, cmd/child; cmd/spawn is the
// acceptance run's parent on a laptop). Its own Go
// module, as iroh-transport/ is, so protocol/ never imports a platform
// SDK (ADR-0009). Everything but cmd/ is C-free and tests untagged;
// cmd/ binds iroh behind build tag `iroh`, compiled and tested only by
// `nix build .#actors-bin` (AGENTS.md's quality-gate caveat applies) —
// as is relaygate's one tagged test, which runs the gate in front of
// the real relay (`scripts/test-iroh.sh actors` locally).
package actors
