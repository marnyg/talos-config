# Notes — sovereign-actor protocol

<!-- "Weather, not climate" for the protocol scope. Each entry
     `YYYY-MM-DD — <note>`. Deployment weather lives in the root notes. -->

- 2026-09-25 — **An actor id does not fit a k8s label value** (`:`
  and 67 chars vs. a 63-char `[A-Za-z0-9._-]` value). Anywhere an id
  must ride on a k8s object, use an annotation; only the 16-hex lease
  id is label-shaped. Docker labels have no such limit. Also: the
  cluster's namespaces enforce PodSecurity `restricted` — any pod a
  driver creates needs the full non-root/seccomp/drop-ALL contexts,
  and so the child image must run as non-root.
- 2026-09-25 — **Docker's "image missing" 404 has two spellings**:
  `No such image: …` (classic store) and `no such image: …: image
  not known` (containerd store, Docker Desktop here). The containerd
  store also normalises image references on list
  (`docker.io/library/…`). Compare digests, not names.
- 2026-09-22 — **The protocol's real consumer is a build away, and it
  is not in `go test ./...`.** `config-server`'s iroh suite (`-tags
  iroh`, cgo) is what exercises a real chain end to end, and it runs
  only inside `nix build .#config-server-bin` / the hub image — so a
  protocol change can pass everything under `protocol/` and still
  break the fleet (`ydq0` did, for three commits). After touching
  `cert/` or `actor/`, run it: `CGO_ENABLED=1
  CGO_LDFLAGS=-L$(nix build .#iroh-ffi-static --print-out-paths)/lib
  IROH_RELAY_BIN=$(nix build .#iroh-relay --print-out-paths)/bin/iroh-relay
  go test -tags iroh -count=1 ./...` from `config-server/`.
- 2026-09-22 — **"Receiver-signed" does not mean "the receiver's
  consent."** An actor whose hot key speaks as its sovereign
  (ADR-0018) signs ordinary *links* with the same key it signs
  consents with. Any rule of the form "treat a receiver-signed cert
  as X" is therefore only decidable by the receiver, which alone knows
  its `Consents`. `ydq0` is the worked example; suspect the same shape
  in anything else that inspects `c.Iss` caller-side.
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
  _Update 2026-09-22: there is — `Actor.Verbs[facet]`, absent ⇒
  invoke; `lighthouse.New` sets `#publish → publish` (ADR-0007)._
- 2026-09-22 — **Envelope canonical form has one optional key**:
  `"postage"` is omitted when empty (ADR-0007). Every other key is
  still always present. A decoder older than this rejects a *stamped*
  envelope (unknown key) — only frontdoor traffic carries one.
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
- 2026-09-19 — `actor.SeqBase` is opt-in; tests that `Peek` the HWM
  after a fresh actor's first send expect `1`. A consumer that seeds
  from the clock must seed *before* its first `Send` (it is read on
  the first send to each receiver, then ignored).
- 2026-09-19 — **JCS numbers are doubles: any envelope/cert field that
  is a large integer is a hazard.** `seq` is now capped at
  `envelope.MaxSeq` (2^53−1) and refused out of range on both sides
  (ADR-0006). `iat`/`exp` are Unix *seconds* (~1.8e9) and safe; the
  same is not true of anything nanosecond-scaled. When adding a numeric
  field, round-trip it through `Encode`/`Decode` in a test, not just
  through the struct.
- 2026-09-23 — **Consumers do not vendor `protocol/lighthouse`**: a
  change confined to it (or to `_test.go` files) leaves both
  `vendorHash`es unchanged; a change to `actor`, `cert`, `envelope`,
  `postage` or `clock` does not. The pre-push hook's `--rebuild` is
  still the arbiter — `nix build .#<attr>.goModules` alone is a cache
  hit even when stale.
- 2026-09-23 — `cert.VerifyChain` with several rooting consents now
  prefers a postage-free non-group verdict; a test that asserts
  `eff.Cav.Postage` on a receiver holding both a frontdoor and a named
  consent must present a signer only `"*"` admits to see the stamp
  requirement.
- 2026-09-23 — **Under a frozen fake clock, same-shape certs are
  byte-identical** (Ed25519 is deterministic; iat/exp are the only
  per-mint fields). A test that assumes two mints are distinct certs
  must vary a field or `Advance` the clock between them — found when
  two `Spawn`s in one test shared a birth consent. Not a test-only
  fact: the spawner dedupes on install and refcounts on drop for it.
- 2026-09-23 — **A mutation test caught the frozen-clock trap the same
  day it was noted**: `TestSpawnBirth`'s second beat passed with the
  `6sax` fix commented out because the first renew re-issued a
  byte-identical cert. Rule of thumb for renewal tests: `Advance` the
  clock before every beat and assert `fresh.Sig != old.Sig` as a
  premise.
