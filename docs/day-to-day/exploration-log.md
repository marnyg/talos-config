# Exploration Log

<!-- "What we've tried and ruled out." Prevents re-attempting dead ends across sessions.
     Granularity: strategy-level pivots only. Not "used ripgrep instead of sed".
     Yes: "tried library X, ruled out for reason Y." -->

## Mesh v2 — hub DNS mechanism

- 2026-07-29 — Nebula built-in lighthouse DNS (`serve_dns`) for the
  embedded hub. Ruled out: it binds a kernel UDP socket (dns_server.go
  requires an active TUN to bind the overlay IP); on the TUN-less
  `nebula/service` hub, overlay UDP/53 lands in the gvisor netstack and
  is dropped (verified empirically on the spike). Landed on: port the
  existing wgdns pattern (git-derived zone, gonet UDP listener on the
  netstack); needs a small variant of the TCP-only service package.
  Recorded in ADR-0002 consequences — delete this section when the
  phase-1 implementation lands.
