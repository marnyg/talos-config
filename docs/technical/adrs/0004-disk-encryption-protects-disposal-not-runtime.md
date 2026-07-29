# ADR-0004: Disk encryption protects disposal, not a running machine

- Status: Proposed _(records a decision taken 2026-07-24; never written up)_
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

### Confirmation

Right while the realistic threat is disk disposal and the hub re-seals
per deploy. Revisit if either changes: if machines move outside the
owner's physical control, Option A's cost becomes worth paying — and the
prerequisites above become the work item. Invalidated if anyone ever
relies on slot-0 revocation as a real access control.
