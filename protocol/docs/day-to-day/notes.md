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
  ~94 s (was ~20 s). Nightly tier only. _(`check.sh` comment verified
  current 2026-09-13, `djs`.)_
- 2026-09-13 — `envelope.Verify` returns `Result{}` on chain reject,
  so `actor` captures `verified` in its `ChainVerifier` closure to feed
  `clock.Mark`. _Resolved 2026-09-13 (`kp4`): `Verify` returns
  `Result{Verified}` beside `ErrChain`; the closure is gone._
- 2026-09-13 — `iroh-transport/` tests need `CGO_LDFLAGS` +
  `IROH_RELAY_BIN` by hand (README); under nix, `nix build
  .#iroh-transport`. The `-static` attr is Linux-only and unverified.
- 2026-09-13 — **Importing `iroh-go/nix` with `pkgs = pkgsStatic` makes
  every `pkgs.*` helper static too** — the cargo vendor fetcher's python
  helper lost `requests` that way (CI 34724490214). Anything
  target-independent in that file (vendor, source prep) must come from
  `pkgs.buildPackages`; `nix eval` the static and native
  `iroh-ffi.cargoDeps.drvPath` — they must be identical.
- 2026-09-13 — `clock.Mark` now locks internally; `go vet`'s copylocks
  will flag a `Mark` passed by value. `actor.process` verifies outside
  `a.mu` (`HWM` and `Mark` self-guard; `Consents` is config).
- 2026-09-12 — `protocol/` must not import `iroh-go` (doc.go, iroh-go
  README). The iroh `Transport` is its own module (`0bc.2.6`).
