# ADR-0028: The break-glass passphrase binds to the install, not the config selector

- Status: Accepted (2026-10-04)
- Date: 2026-10-03
- Amends: ADR-0004 (slot 1 stays; what it derives from changes)

## Context and Problem Statement

LUKS slot 1 — the derived break-glass passphrase ADR-0004 keeps so a
sealed hub never blocks a boot — was `RecoveryPassphrase(master, MAC)`,
the MAC being the directory `talos/machines/<mac>/` the install config
was fetched from. On 2026-10-01 (`c4vd`) w1's directory was renamed to
its dongle's MAC so a reinstall would find its config; the note said
"nothing else is keyed by the directory MAC under v3". The hub then
composed a different slot-1 passphrase for w1 on every serve, and
`recover -recovery` derived the wrong one. Nobody saw it until the
2026-10-03 re-serve dry-run showed the passphrase diff and *"with a
reboot"* — on the node holding the media library's only replicas.
Talos re-keys LUKS to a changed config only at boot and only after a
configured key opens the volume; the only such key would have been
slot 0 (KMS), which ADR-0004 recorded as dormant at boot. A changed
passphrase was a disk that might not open.

## Decision Drivers

- The passphrase lives as long as the LUKS header it is in (the
  install). Its derivation input must live at least that long.
- Offline recoverability (invariant 3, ADR-0018): the input must be
  something the owner can read off a dead machine — no hub, no state.
- Invariant 6: the MAC *selects* configuration; invariant 7: ephemeral
  facts are never baked into durable identity. A NIC's address is both
  a selector and, as w1 showed, replaceable.
- Three installed headers hold MAC-derived passphrases today, and
  re-keying them is not safe until slot 0 at boot is trusted.
- Frozen derivation contract (`masterderive` package comment): a
  changed info string re-keys the fleet; v1 must stay available.

## Considered Options

### Option A: Derive from the SMBIOS UUID; grandfather installed nodes explicitly

`RecoveryPassphrase(master, uuid)` under a new info string (v2); the
v1 function stays as `RecoveryPassphraseMAC`, frozen. `meta.yaml`
gains `installMAC:` — the MAC a pre-2026-10-03 install was keyed
under — and the hub derives from it when present, from the UUID
otherwise, refusing an encryption block when neither exists. The
field is transitional: deleted at the node's reinstall, or at a
re-key once slot 0 at boot is proven (`spvd`).

- Pros: the UUID is the chassis, already the KMS allowlist (declared
  before the install config is served), readable from the service
  tag offline. Installed nodes keep the exact passphrase on disk; the
  exception is visible per machine in git and has an exit.
- Cons: a second schema field to explain; OEM-placeholder UUIDs (nas1)
  make two such boxes under one master share a passphrase — the same
  owner-local caveat the KMS allowlist already accepts.

### Option B: Keep MAC derivation, document "never rename a directory"

- Pros: no code.
- Cons: the rename is *required* for a reinstall after a NIC swap
  (the node fetches `/config?mac=<NIC it booted on>`); the rule would
  conflict with the runbook and the secret would remain bound to a
  replaceable part.

### Option C: Derive from the UUID for every node, re-key by reboot

- Pros: one rule, no field.
- Cons: three reboots behind an unlock path ADR-0004 does not trust,
  on nodes with single-replica volumes. Becomes Option A's exit once
  `spvd` lands.

### Option D: Random passphrase per install, stored by the hub

- Cons: state the hub must keep durable — ADR-0018 makes hub actors
  ephemeral-key and stateless; recovery would need the hub.

## Decision Outcome

Chosen: **Option A**. The rule it encodes, worth stating beyond this
case: *a secret derives only from a handle that outlives it.* The hub
test `TestDiskEncryptionSurvivesRename` pins the property (same
declaration under a new directory → byte-identical
`systemDiskEncryption`), and `nix run .#apply` dry-runs first and
refuses an encryption diff outright, so the next drift is loud at the
operator's terminal rather than at a node's boot.

### Consequences

- New installs: `uuid` is required for `diskEncryption`; `recover
  -recovery -uuid <uuid>`. Grandfathered: `recover -recovery -mac
  <installMAC>`.
- `apply` needs `APPLY_REBOOT=1` for any reboot-requiring change —
  a re-serve is expected to be reboot-free.
- Follow-ups: `spvd` (prove slot-0 unlock at boot under v3; then one
  reboot per node and delete the three `installMAC` fields).
  Invariant 7 carries the rule explicitly since `10ebcbc`.
- _(2026-10-04)_ **Exit taken.** `spvd` proved slot 0 live at boot
  (ADR-0004 amendment) and all three `installMAC` fields are gone;
  every installed header now holds the UUID passphrase. The ceremony
  turned out to be **two boots per node, hub unsealed throughout**:
  Talos writes the STATE encryption config to META only after STATE
  is open, so boot A opens STATE on the old slot 1, updates META and
  re-keys EPHEMERAL (whose config comes from the machine config), and
  boot B rejects the old slot 1 on STATE, opens it via KMS ~30 s later
  and re-keys. `nix run .#apply` needs `APPLY_REKEY=1 APPLY_REBOOT=1`
  for boot A; boot B can be any reboot (here: the `talosctl upgrade`
  to p0agent 0.1.6). The grandfather code path (`installMAC`,
  `RecoveryPassphraseMAC`, `recover -recovery -mac`) stays frozen per
  the derivation contract but no machine uses it.

### Confirmation

Right if: no `systemDiskEncryption` diff appears in a dry-run for an
installed node again; a reinstall after a NIC swap needs only the
directory rename; `spvd` retires the fields within a few months
_(done the next day, 2026-10-04)_.
Invalidated if: a chassis swap with the same disks turns out to be a
real operation (the UUID would move away from the header) — then the
handle should be the install itself (an ID minted at install and
written to META), not the chassis.
