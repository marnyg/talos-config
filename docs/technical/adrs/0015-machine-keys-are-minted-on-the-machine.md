# ADR-0015: Machine keys are minted on the machine; hardware selects configuration, not identity

- Status: Proposed — implementation: `talos-config-359.8.3` (identity
  plane, Mesh v3) or `talos-config-7ci` (nebula fallback if the Phase 0
  gate fails); promote to Accepted when either lands. Design record:
  spike `talos-config-px2` (closed 2026-09-04).
- Date: 2026-08-16
- Amends: invariants 1 and 6 (see `desired-state/invariants.md`)
- Related: ADR-0012 (device-born device keys), sketch `6a80633d`,
  `docs/sovereign-actor-protocol.md` (design lens)

## Context and Problem Statement

Machines are the last members whose private keys the hub can compute.
`nebderive.MachineKey(master, MAC)` derives each node's X25519 key and
bakes it into the Talos config served at `GET /config?mac=…`; the key
travels the wire on every config serve and exists in hub memory at
compose time. ADR-0012 refused exactly this for devices ("born
on-device, never travel"), leaving the mesh with two identity
lifecycles — `nebderive` for machines, `devkey`/enrollment for devices
— a permanent conceptual and code split.

Reviewing the domain model against the sovereign-actor design sketch
made the split's justification look thin. What hub-derived machine
keys actually buy is **unattended reinstall** (an approved machine
rebuilds from nothing, no human act) and zero-ceremony whole-fleet
recovery. Everything else credited to derivation holds without it:

- **Addressing/DNS never move on re-key.** `MachineIP(master, MAC)`
  derives the address from the MAC, not the key — same property
  ADR-0012 relies on for devices (name-derived there, MAC-derived
  here).
- **The machine set stays enumerable from git** (`talos/machines/
  <mac>/` directories) regardless of where keys are born.
- **KMS/disk keys** derive from the master per (machine, partition),
  independent of the mesh keypair.
- **Impersonation resistance** was never provided by derivation — the
  CA can mint a cert for any name either way.

Meanwhile the costs are real: the hub can silently *be* any machine
(not merely admit one); the MAC is a leaky identity anchor (a NIC swap
is a "new machine" today); and the two-lifecycle split doubles the
identity vocabulary and code.

One observation dissolves most of the trade: **today's de facto
admission act is already "able to fetch config"** — the served config
contains the private key itself. Any mechanism that puts a *single-use
enrollment token* in the served config instead of the key is strictly
no worse in exposure, and preserves unattended reinstall.

## Decision Drivers

- One member identity concept: keypair minted on the member,
  membership = a CA-signed cert, revocation = expiry (the ADR-0012
  model, and the sovereign-actor "birth handshake" shape).
- Private keys never travel; the hub mints certs, never keys.
- Reinstall/recovery ergonomics must not regress: a wiped, approved
  machine still rejoins without a human act.
- Invariant 2: no new durable hub state — the token flow must be as
  stateless as device enrollment.
- The hardware anchor's real job is *ratification and config
  selection* (which named config does this box get), not key material.

## Decision Outcome

Machines adopt the device identity lifecycle, with boot-time
enrollment replacing baked-in keys:

1. **Config serve injects an enrollment token** (short-TTL, bound to
   the MAC/name, carrying a random nonce, HMAC-derived from the master
   so verification is stateless — no pending table) instead of a
   private key. **Single-use per hub process, TTL-bounded**: the hub
   keeps only a volatile seen-set, so a token copied from a served
   config can be redeemed again after a redeploy inside its TTL. That
   residual is accepted — it is strictly less exposure than today's
   config, which contains the key itself. The nonce is required: two
   serves in one time bucket would otherwise be the same token and
   the seen-set would reject a legitimate wipe-and-reboot. _(Reworded
   2026-09-05 from `approval.qnt`, `vzj`; "single-use" without
   qualification was refuted.)_
2. **The node mints its keypair at first boot** (ext-nebula) and posts
   `{pubkey, token}` to a hub enrollment endpoint over HTTPS —
   provisioning-plane, honoring invariant 4.
3. **The hub verifies the token and mints the cert**: name and groups
   from git (`meta.yaml`), address from `MachineIP(master, MAC)` —
   nothing moves across re-keys or reinstalls. 90-day validity, same
   renewal story as devices (task `49443c38` covers both).
4. **Hardware narrows to configuration selection**: the MAC picks
   `talos/machines/<mac>/`; approval (invariant 6) ratifies the
   name↔hardware binding, single-use, as today. Keys are no longer a
   function of hardware.

The stricter variant — one wallet signature per reinstall, no token in
config — was considered and set aside: it degrades 3am self-heal for
no exposure gain over the chosen shape (fetching config is gated the
same either way, and no longer yields a key at all, only a short-lived
token redeemable once for a cert whose properties git dictates).

## Considered Options

- **Status quo** (hub-derived keys) — rejected: keys travel, hub can
  impersonate members, two lifecycles forever.
- **Signature-per-reinstall enrollment** (device flow for machines) —
  rejected as default: loses unattended recovery; acceptable fallback
  when a token can't be trusted (e.g. config channel compromise
  suspected).
- **Token-in-config boot enrollment (chosen)** — key exposure strictly
  reduced, recovery ergonomics preserved, one lifecycle.

## Consequences

- Invariant 1's machine scope narrows: re-derivable without
  re-enrollment = hub, CA, addresses, DNS, configs — **not member
  private keys**. Machine keys join device keys as born-on-member.
- Invariant 6 rewords: hardware-anchored *configuration*
  ("talos config from MAC"), human-ratified binding; identity keys
  explicitly excluded from the anchor.
- Whole-fleet disaster recovery changes shape: instead of recomputing
  keys offline, each machine re-enrolls at boot through the same
  automatic path. The hub + CA remain recomputable from (git, wallet)
  alone.
- The domain model's "two identity lifecycles" section collapses to
  one lifecycle with one axis (who ratifies: MAC-bound approval vs
  wallet-signed enrollment); update `desired-state/domain-model.md`
  when implementation lands.
- Implementation touches: config-server compose path (strip
  `MachineKey`, inject token), new enrollment endpoint (shares the
  device verify+mint core), ext-nebula first-boot keygen + redeem,
  migration for cp1/w1/tv (re-key in place or at next reinstall).
- Until implemented, the mesh runs in the old model; this ADR marks
  the desired state, like ADR-0012 did during its in-flight window.
