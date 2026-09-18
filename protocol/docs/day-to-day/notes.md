# Notes — sovereign-actor protocol

<!-- "Weather, not climate" for the protocol scope. Each entry
     `YYYY-MM-DD — <note>`. Deployment weather lives in the root notes. -->

- 2026-09-13 — **ADR-0001 is Accepted and built** (`0bc.2.1–.6`).
  `cert.VerifyChain` is the one verifier; `Authorize` calls it per
  grant. **Still change `verification/quint/authorize.qnt` before the
  Go** — `authorize_chain_laws_test.go` pins the 18 chain laws 1:1.
  Canonical cert form changed (`"postage":""` always present): certs
  signed before `40c1755` no longer verify (none existed in-repo).
- 2026-09-17 — **`cert.VerifyChain` takes a `verb` parameter** (xwu,
  ADR-0002). Callers name the verb the operation expects; `actor` binds
  `invoke` via `invokeChain`. A new facet that expects another verb
  (M3 `#publish`) must bind its own — there is no facet→verb table yet.
- 2026-09-18 — **`cert.VerifyChain` takes a `cert.Receiver`** (kau,
  ADR-0003): `{ID, Consents, SpeakAs}` — everything the receiver brings
  itself. `Receiver.SpeakAs` (held, trusted) and the caller's
  `speakAs`/`Bundle.SpeakAs` (presented, untrusted) are both speak-as
  sets with opposite roles; the type is what keeps them apart. Pass the
  actor's whole `SpeakAs` — the verifier filters `aud == ID`.
- 2026-09-13 — `quint verify` on `authorize.qnt` at depth 2 is now
  ~94 s (was ~20 s). Nightly tier only. _(`check.sh` comment verified
  current 2026-09-13, `djs`.)_ _Update 2026-09-17: ~135 s since the
  chain verb became a scenario variable; `check.sh` note refreshed._
  _Update 2026-09-18: ~165–170 s since `cav.target` may name a
  principal the receiver answers for (kau); ~173 s with the ADR-0004
  wildcard target sets (zeb)._
- 2026-09-13 — `envelope.Verify` returns `Result{}` on chain reject,
  so `actor` captures `verified` in its `ChainVerifier` closure to feed
  `clock.Mark`. _Resolved 2026-09-13 (`kp4`): `Verify` returns
  `Result{Verified}` beside `ErrChain`; the closure is gone._
- 2026-09-13 — `iroh-transport/` tests need `CGO_LDFLAGS` +
  `IROH_RELAY_BIN` by hand (README); under nix, `nix build
  .#iroh-transport`. The `-static` attr is Linux-only; green in CI
  since 2026-09-13 (musl, fully static, 6/6).
- 2026-09-13 — **`iroh-transport` `vendorHash` covers `../protocol` and
  `../iroh-go/iroh`** (local `replace`s are vendored). Any change to
  those trees changes the hash, and a cached FOD output hides it until
  a cold builder rebuilds — after touching them run
  `nix build .#iroh-transport.goModules --rebuild`.
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
