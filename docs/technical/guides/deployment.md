# Deployed state

Where the running system stood when last verified. Rehomed from the
legacy `docs/handover.md` so it survives that file's deletion. Facts here
decay — each block carries the date it was last confirmed. If you verify
or change something, update the date.

## Cluster — _last verified 2026-07-24_

- Single control plane `b0:41:6f:15:3b:8f`, Talos v1.12.6, k8s v1.32.3.
- LUKS2 on STATE + EPHEMERAL.
- Cluster endpoint `https://10.99.0.54:6443` — the node's **derived
  tunnel IP**, deliberately not its DHCP LAN address (invariant 7; DHCP
  handed out four leases in one day before this moved).
- Media stack 6/6 Running. SealedSecrets (`newshosting`, `nzbgeek`)
  unseal via the inlineManifest-provisioned key pair.
- Admin access is tunnel-only: `talos/talosconfig` (local, gitignored)
  points at 10.99.0.54; NodePorts serve on wg0 (Jellyfin
  `10.99.0.54:30096`).

## Hub on fly — _last verified 2026-07-29_

- `fly secrets list` is **empty**. Everything derives from the wallet
  signature at unseal: wg keys (server/machine/admin), tunnel IPs, KMS
  seal keys, recovery passphrases, the age identity that decrypts
  `clusters/**/*.age` into tmpfs, and — since 2026-07-29 — the nebula
  mesh CA and all mesh identities.
- Public age recipient is committed at `talos/age-recipient.txt`
  (re-derive with `wgping -age-recipient -sig <unseal-sig>`). The SSH key
  remains a break-glass recipient.
- An unseal that cannot decrypt the secrets fails loudly rather than
  serving broken configs.

## Disk encryption posture — _decision, closed 2026-07-24_

Slot 0 is the network KMS, slot 1 a derived static passphrase stored in
plaintext META.

- **Slot 0 is dormant at boot in practice**: early-boot DNS loses the
  race to the KMS dial every time so far. Accepted rather than fixed.
- **Accepted consequence**: encryption protects against disk
  disposal/RMA only, not against an attacker with the running machine.
- A sealed hub therefore does **not** block reboots — slot 1 boots the
  node unattended. Only provisioning and config refetch need an unseal.
- Going KMS-only would first require break-glass tooling for slot-0
  blobs.

> Now recorded properly in **ADR-0004**, including the consequence that
> matters most: wipe META before a *machine* (not just a disk) leaves the
> owner's hands, because the slot-1 passphrase travels with it.
