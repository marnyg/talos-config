# ADR-0004: Disk encryption protects disposal, not a running machine

- Status: Accepted _(records a decision taken 2026-07-24; written up
  2026-07-29; amended 2026-10-04 — slot 0 is live at boot, see the
  last Consequences bullet)_
- Date: 2026-07-29

## Context and Problem Statement

Talos node disks (STATE + EPHEMERAL) are LUKS2-encrypted with two key
slots: slot 0 unseals over the network from the hub's KMS (per-boot
auth, revocable server-side by deleting the machine's UUID), slot 1 is a
derived static passphrase written into plaintext META.

In practice **slot 0 never unseals at boot**: early-boot DNS loses the
race to the KMS dial every time observed so far, and slot 1 boots the
node instead. That was accepted rather than fixed — but "the disks are
encrypted" then means something much weaker than it sounds, and the gap
between the intent and the reality has never been recorded. This ADR
records what the posture actually buys.

## Decision Drivers

- Invariant 4: nothing on the boot or recovery path may depend on
  services that might be down. A node must boot unattended.
- Invariant 3: recovery must work from LAN with owner-held keys, without
  the hub.
- Invariant 8: secrets plaintext only in memory — but META is on the
  disk being protected, which is the crux here.
- A homelab's realistic threat is a disk leaving the house (RMA, resale,
  disposal), not an attacker with physical access to a running machine.
- Every fly deploy re-seals the hub. If booting required the hub, a
  deploy plus a power cut would strand the cluster.

## Considered Options

### Option A: KMS-only (slot 0, no static slot)

Drop the static passphrase; the node cannot boot without the hub
authorizing it.

- Pros: genuinely strong — a stolen disk is inert, and revocation is
  real (delete the UUID, the node never boots again).
- Cons: the hub becomes a hard boot dependency, violating invariant 4;
  a sealed hub plus a reboot strands the cluster; needs break-glass
  tooling for slot-0 blobs before it could be trusted, which does not
  exist. Early-boot DNS already loses the race, so this would turn a
  cosmetic wart into an outage.

### Option B: Static passphrase only

Skip the KMS entirely.

- Pros: simplest; boots unattended.
- Cons: gives up per-boot authorization and server-side revocation
  permanently, with nothing gained over Option C.

### Option C: Both slots, static as the effective path (chosen)

Slot 0 KMS + slot 1 derived static passphrase in plaintext META. The
node boots from slot 1; slot 0 stays configured and dormant.

- Pros: boots unattended; keeps the KMS path present so tightening later
  is a config change, not a rebuild; the passphrase is wallet-derivable
  offline (`wgping -recovery`) and stored nowhere, so recovery needs only
  the wallet.
- Cons: the passphrase sits in plaintext on the same disk it protects, so
  encryption is worthless against anyone holding the powered-off machine
  with its META intact. Per-boot authorization and revocation are
  nominal, not real.

## Decision Outcome

Chosen: **Option C**, because invariant 4 outranks the strength of the
encryption here. A cluster that cannot boot without a hub that re-seals
on every deploy is a worse failure than a disk whose key is on itself.

The honest framing: **disk encryption on this cluster is disposal and RMA
protection only.** It is not runtime protection and not theft protection
against someone who takes the machine.

### Consequences

- Safe to RMA or discard a disk without wiping it: the data is
  unreadable without the META partition.
- **Not** safe to assume a stolen or resold *machine* protects anything.
  Wipe META (or the whole disk) before a machine leaves the owner's
  hands — the passphrase travels with it otherwise.
- A sealed hub does not block reboots. Only provisioning and config
  refetch need an unseal, which is the intended blast radius.
- Revocation via UUID deletion is only meaningful for machines that
  actually reach the KMS — i.e. currently none at boot. Do not treat it
  as an access control.
- Tightening to KMS-only stays open but is gated on: fixing early-boot
  DNS (or dialing the KMS by IP to sidestep resolution entirely) *and*
  building break-glass tooling for slot-0 blobs.
- _(Noted 2026-09-06.)_ **Unseal grace window is an accepted residual.**
  `kms.go` unseals blobs for UUIDs not yet in `machines/<mac>/meta.yaml`
  if this hub *process* sealed them (`sessionSealed`), so a fresh
  install can reboot before the admin records the UUID. That set is
  volatile hub memory — the same shape as ADR-0015's replay guard — and
  is not in `approval.qnt`. Accepted because Seal is open anyway and the
  static slot is the effective boot path: the window grants nothing the
  disk does not already grant. Revisit if KMS-only ever lands.
  _(Refiled from the running code, not a new decision.)_
- _(Noted 2026-09-22, nas1.)_ **A node UUID need not be unique, and one
  allowlist entry can cover several machines.** nas1 (TerraMaster
  F4-425 Plus) ships the OEM's unprogrammed SMBIOS: serial `Default
  string`, UUID `03000200-0400-0500-0006-000700080009` — a value every
  never-programmed board of that line presents. Consequences, none of
  which change this ADR's posture: the seal key `KDF(master, uuid)` is
  shared by any such board, so a second one would have to be declared
  under the same value and **deleting it would revoke both**; and the
  UUID is that much weaker as a label for "which machine". None of this
  weakens the decision itself, because the UUID was never an
  authenticator — it is a string the caller claims in the gRPC request,
  which is exactly why the bullet above says it is not access control.
  The rule that does the work is unchanged: **wipe META before a machine
  leaves the owner's hands.** Recorded in that machine's `meta.yaml`;
  do not read a node UUID as an identity anywhere else.
- _(Amended 2026-10-04, `spvd`.)_ **Slot 0 is live at boot under Mesh
  v3; "dormant" was a reading of the first attempt only.** Every boot
  log on all three nodes shows the same shape: the *first* volume's
  slot-0 call fails ~3 s after kernel start with `network is
  unreachable` (DHCP is not up; it was never a DNS race specifically),
  slot 1 opens it, and the KMS is reachable 2–5 s later — EPHEMERAL
  routinely opens on slot 0, and Talos's post-open `syncKeys` re-keys
  whichever slot it could not verify (`updated encryption key`). Talos
  v1.12 retries a volume whose handlers *all* fail (`Retryable`, 30 s
  ticker) rather than giving up. Proven 2026-10-04 on nas1 and w1 by
  changing the slot-1 passphrase (ADR-0028's exit): slot 1 `encryption
  key rejected` → ~27 s → `opened encrypted device slot 0
  *keys.KMSKeyHandler` → `updated encryption key slot 1`. What this
  changes: a slot-1 re-key is a safe, two-boot operation **while the
  hub is unsealed** (boot A: the new config lands in META after STATE
  opens on the old passphrase, EPHEMERAL re-keys; boot B: STATE
  re-keys), and the slot-0 blob is a real second key, not a nominal
  one. What it does not change: slot 1 stays, because a sealed hub
  must still not block an unattended reboot (invariant 4) — Option A's
  cost is unchanged. The `apply` guard refuses an encryption diff
  unless `APPLY_REKEY=1` says the ceremony is intended.

### Confirmation

Right while the realistic threat is disk disposal and the hub re-seals
per deploy. Revisit if either changes: if machines move outside the
owner's physical control, Option A's cost becomes worth paying — and the
prerequisites above become the work item. Invalidated if anyone ever
relies on slot-0 revocation as a real access control.
