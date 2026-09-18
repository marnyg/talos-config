# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-18 (third session) — **`Issuer#bundle` built** (`359.8.2.3`
part 2; commit `cedf639`). The recipe reaches a caller for the first
time.

- **`config-server/issuer/bundle.go`**: `#bundle {member: <cert>}` →
  `Bundle {grants[], blocklist[], speak_as}`. The Issuer verifies the
  member cert itself (verb, sig, unexpired, `aud == From`, issuer =
  this hubkey **or** a dead hubkey resolved via `cert.SpeaksFor` over
  the proof's speak-as from *its* wallet), then `policy.Compile` for
  `cav.name`/`cav.groups` and signs each grant with the hot key.
  `PolicySource` / `FilePolicy(root)` reads recipe + blocklist from
  the checkout on every beat; wired in `hubseal.go`.
- **Blocklist on the beat** (`j0b`): `talos/mesh-blocklist-v3.txt`
  (ed: ids; `policy.LoadBlocklist`, strict parse) rides every bundle;
  a listed caller is refused at `#renew` **and** `#bundle`, so its
  certs run out.
- **Kit grant → `BeatGrant`** with `facet: [#renew, #bundle]` (wire key
  `beat_grant`); the wallet consent widens the same way
  (`issuer.BeatFacets`). Decision `1tg` records the `{}`→`{member}`
  payload deviation from ADR-0024 (proof chains are invoke-only).
- Tests: `bundle_test.go` (compile-for-member + admits at a node
  receiver, refusals, wire, beat across an Issuer rotation over
  `MemoryNetwork`); `policy.TestBlocklist`. All `config-server` green.

## Loose threads

- `359.8.2.3` stays **in_progress**: the **name map** half of `#bundle`
  waits on `e8d` (the location cache needs real iroh endpoints); the
  "hub-http shrink to /config" is spec-only — no hub-http stream facet
  exists in code to shrink.
- **Nobody calls `#bundle` yet**: no member client exists (`359.8.3`
  cp1 agent, `359.8.4` irohup) and the Issuer's transport is in-memory
  until `e8d`. A `mesh-policy-v3.yaml` edit still changes nothing at
  runtime.
- Docs proposals pending user confirmation (see this session's report):
  domain-model Issuer row + Kit glossary drift, a **Bundle** glossary
  disambiguation (connect-time bundle vs `#bundle` reply), a
  **Blocklist (v3)** glossary line, an ADR-0024 amendment note.
- The Issuer refusing `#renew` to a blocklisted key reads git inside
  an inbound-call decision. Judged as the *compiler* declining output
  (invariant 2 names receivers as the parties that never read git),
  not surfaced as a violation — veto if wrong.
- Carried: `tqr`, `kql` (blocked on `359.8.3`), no graceful shutdown in
  `config-server`, `DefaultMailbox = 64` / renewal-beat fraction
  unbeaded, w1 down (`0q0`, `kso`), `5gz` cold-cache trap.

## Suggested next steps

- **`e8d`** — the hub binds its own iroh Endpoint (fly build change:
  cgo + `libiroh_ffi`); then `Issuer.Listen` on iroh and the name map
  half of `#bundle`.
- **`359.8.3`** cp1 agent: consumes `policy.AcceptTable(KindNode)`,
  runs the beat (`#renew` + `#bundle`), replaces its blocklist copy
  from the bundle; `kql` tears the scratch relay down after.
- Promote ADR-0024 toward Accepted once `e8d` lands (Provisioner as an
  actor is the other outstanding item).
