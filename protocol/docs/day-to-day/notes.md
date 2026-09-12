# Notes — sovereign-actor protocol

<!-- "Weather, not climate" for the protocol scope. Each entry
     `YYYY-MM-DD — <note>`. Deployment weather lives in the root notes. -->

- 2026-09-13 — **ADR-0001 is Accepted and built** (`0bc.2.1–.6`).
  `cert.VerifyChain` is the one verifier; `Authorize` calls it per
  grant. **Still change `verification/quint/authorize.qnt` before the
  Go** — `authorize_chain_laws_test.go` pins the 18 chain laws 1:1.
  Canonical cert form changed (`"postage":""` always present): certs
  signed before `40c1755` no longer verify (none existed in-repo).
- 2026-09-13 — `quint verify` on `authorize.qnt` at depth 2 is now
  ~94 s (was ~20 s); `check.sh`'s comment is stale. Nightly tier only.
- 2026-09-13 — `envelope.Verify` returns `Result{}` on chain reject,
  so `actor` captures `verified` in its `ChainVerifier` closure to feed
  `clock.Mark`. Don't "simplify" that closure away until `Verify`
  returns `Verified` with the error.
- 2026-09-13 — `iroh-transport/` tests need `CGO_LDFLAGS` +
  `IROH_RELAY_BIN` by hand (README); under nix, `nix build
  .#iroh-transport`. The `-static` attr is Linux-only and unverified.
- 2026-09-12 — `protocol/` must not import `iroh-go` (doc.go, iroh-go
  README). The iroh `Transport` is its own module (`0bc.2.6`).
