# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-08-21 — **Formal law-checking landed** (beads epic
`talos-config-7wg`): the design laws implicit in the invariants are
now mechanically checked at two levels.

- **rapid property suites** (`config-server/{mesh,nebderive,
  masterderive}/*_prop_test.go`): overlay-replaces-wholesale
  (ADR-0014) through the real Manager path, master derivation
  determinism + recovery-byte independence, addr = f(normalized name)
  (re-enrolling never moves a member), CA/leaf cert byte-stability
  (invariant 2 at the cert level). Pure `composeEffective` extracted
  from `Manager.effectivePolicy` to name the ADR-0014 law in code.
- **Quint design models** (`verification/quint/{hub,enroll,
  approval}.qnt` + `check.sh`): seal lifecycle with crash-anywhere,
  device enrollment (nonce single-use, certs survive redeploy,
  ADR-0012 non-derivability as absence), machine approval as an
  affine resource (invariant 6). All verify under Apalache (hub,
  approval depth 15; enroll depth 8 + 300×30 simulation). Every
  invariant mutation-tested: seeded bugs all caught.
- **Fixed `TestNodeFirewallGrantsHubAndAdminsOnly`** — broken on HEAD
  since `7567336` widened media to :80 without the test update the
  policy file demands; it blocked `nix build .#config-server-bin`.
- `quint` added to the devshell; go vendorHash recomputed (rapid dep).
- **Beads initialized** in this repo (`.beads/`) — replaces
  taskwarrior for new work; legacy migration not yet done.

## Loose threads

- Session proposed adding a short "Laws" section to
  `desired-state/domain-model.md` (the equations the suites check) —
  not yet written, owner to confirm.
- Possible ADR: the two-tier verification choice (Quint + rapid over
  Alloy/TLA+/proofs) — owner to decide if it warrants ADR-0015.
- `nebderive.DeviceKey(master, name)` still mints device keys from
  the master (contradicts ADR-0012's spirit; only `nebtest` uses it)
  — undecided broken window.
- Taskwarrior→beads legacy migration not checked yet this repo.

## Suggested next steps

- 2026-08-22 review session: Quint tasks + epic `talos-config-7wg`
  closed after owner review; prop-test failures routed through
  `*rapid.T`; CI wiring filed as `talos-config-cmi`.
- Write the domain-model "Laws" section (proposal already drafted in
  session transcript).
- Decide the `DeviceKey` question: move into `nebtest` or keep.
