# ADR-0012: Wallet-signed enrollment replaces the declared device list

- Status: Accepted
- Date: 2026-08-05 (proposed) → 2026-08-14 (accepted, implementation in flight)
- Amends: invariant 1 (see `desired-state/invariants.md`)

## Context and Problem Statement

Mesh devices are admitted today by a declared list (`MESH_DEVICES` /
`MESH_MEDIA_DEVICES` in fly.toml): adding a device is a config change
plus a deploy, and the hub derives each device's *private key* from the
fleet master (`DeviceKey = HKDF(master, name)`) and ships it over the
wire at enrollment. That design conflates the device's name (a human
tag and DNS label) with its identity, makes rename a re-key, and puts
every device's whole membership credential in transit and in the hub's
derivation reach. The desired UX is "start VPN, authenticate,
connected" — one wallet signature as the sole admission act, no commit,
no deploy, no key that ever travels.

## Decision Drivers

- Invariant 1: stateless identity — membership must reduce to offline
  wallet verification; no runtime registry the hub "remembers".
- Invariant 2: git is the single source of truth for the control
  plane; a name→group table on the hub is the database the invariant
  says to redesign away.
- A device's private key should be born on the device and never leave
  it — key transit was tolerated in the wg0 era, not a goal.
- Renames and re-keys must not move a device: addresses and DNS labels
  derive from the approved name, so identity can rotate freely.
- One admission ceremony for everything: laptops with a local wallet,
  headless boxes, wallet-less appliances (TV) — not per-device-kind
  asymmetries like `nebtv.go`'s media-only flow.
- The approver, never the device, decides name and group
  (rubber-stamp resistance).

## Decision Outcome

Wallet-signed enrollment is the sole admission act. The device
generates its keypair locally and submits the public key; the approver
sets the final name and group; the wallet signs one canonical message
binding `(name, group, pubkey-fingerprint, nonce)`; the hub verifies
and mints a CA-signed cert (90-day validity, address derived from
name) and returns CA + cert + config skeleton — **never a private
key**. The client splices in its own key. Two entry modes share one
verify+mint core:

- **nebup direct** — wallet local; requester and approver are the same
  person. `-group` flag (default `admins`), `-paste` for headless.
  Two-file cache: `<name>.key` (device-born identity, survives
  `-reenroll`) and `<name>.yml` (disposable hub artifact); `-rekey`
  is the explicit identity-rotation act.
- **RFC 8628 flow** (existing `deviceflow` package) — for devices that
  cannot sign. The start form takes the device's pubkey; the approval
  lives on the existing `/status` dashboard: editable name
  (device-proposed), group radio (default `media`), fingerprint
  displayed, `admins` grants require re-typing the device name, and
  the git-zone collision check runs inline against the *final* edited
  name before the wallet prompt. Pending enrollments stay behind the
  session gate.

Downstream jobs of the old declared list are replaced statelessly:

- **/config gate:** nebula firewall (admins group) stays; the handler
  layer checks the tunnel cert for `groups ∋ admins` *and*
  `cert addr == DeviceIP(master, cert.Name)` (derivation consistency)
  — structurally excludes machines.
- **Device DNS:** devices resolve only while their tunnel is live
  (peer-map match, derived address); machines and the hub always
  resolve from the git-derived zone; enrollment refuses names
  colliding with the git zone's labels or addresses.
- **Rename / group change:** re-enroll under the new name or group.
  The old cert stays valid until its 90-day expiry unless blocklisted
  (blocklist-by-fingerprint, mechanism unchanged). Promotion is one
  signature; demotion is re-enroll **plus blocklist**.

Delivery scope: nebup rework and the hub-side unified flow ship
together. No Android code is built now; the TV/phone APK (task
`2e1bef85`) stays deferred until a remote-TV need appears, and the
phone path is *accepted as possibly nonfunctional until that APK
exists* (the Mobile Nebula import path is opportunistic, unverified
for self-generated keys).

## Considered Options

### Admission mechanism

- **Declared list moved to git** (devices.yaml) — rejected: still a
  commit + deploy per device; the list's four jobs are all replaceable
  statelessly.
- **Wallet-signed enrollment (chosen).**

### Device identity

- **Hub-derived keys** (status quo, `HKDF(master, name)`) — rejected:
  conflates name with identity, ships private keys, re-keys on rename.
- **Device-generated keypair (chosen)** — key never leaves the device;
  name remains the derivation input for address/DNS, so identity
  rotation never moves a device. After a disk wipe the key is gone:
  new keypair, new cert, *same address*.

### Group assignment

- **Device-proposed, approver-ratified** — rejected: rubber-stamp risk
  (a malicious enrollment pre-fills `group: admins`).
- **Dynamically updatable hub-side groups** — rejected: group lives in
  the cert checked at handshake with no hub callback; a mutable
  registry would make membership be the table, against invariants 1–2.
- **Approver-set at mint time (chosen)**; re-enrollment is the update
  mechanism.

### Device DNS

- **Wildcard derivation** (any label resolves) — rejected: typos
  resolve to routable addresses; NXDOMAIN loses meaning.
- **Live-peers-only resolution (chosen).**

### Wallet-less devices

- **`nebtv.go` media-only asymmetry** — superseded: approver-set
  groups let one flow serve all wallet-less devices.
- **nebup admin-mediated mode** (enroll-on-behalf, transfer file) —
  rejected: moves the private key by scp/clipboard, defeating the
  design through the side door.
- **Browser-side keygen** (WebCrypto in the start page) — rejected:
  key generation in the least-auditable runtime, for no consumer that
  needs it.
- **Pubkey submission at flow start (chosen).**

## Consequences

- Enrollment mints no state anywhere; the hub cannot enumerate devices
  and forgets each enrollment. Auditability is the log line, the
  wallet, and the live peer map.
- The 90-day re-sign ceremony per device is now the only recurring
  human act; the persistent device key makes
  a future "re-sign same pubkey" renewal automatable in principle.
- Device↔device address collisions at /16 are accepted as
  probabilistic; enrollment only guards the git zone.
- Signed-message prefix pinned to v1 (see "Signed message (v1)" below);
  future revisions bump the version tail. Thread `72d38fd0` closed.
- The phone has no working enrollment path until either the Mobile
  Nebula import assumption verifies or the APK ships — accepted.

## Signed message (v1)

One canonical text, distinct from the wg enrollment, approval, login
and master-key messages so a signature for one is never replayable as
another. `pubkey-fingerprint` is `sha256(pubkey)` hex — short enough to
read off the approval card, unambiguous, no dependency on nebula's
cert-fingerprint (which does not exist until after the mint).

```
talos config-server mesh device enrollment v1
name: <name>
group: <admins|media>
pubkey: <sha256-hex of the 32-byte X25519 pubkey>
nonce: <server-issued nonce>
```

The old `meshEnrollMessage` (no `v1`, no `group`, no `pubkey`) is
structurally distinct — signatures against it cannot be replayed against
v1. When the message shape needs to change, bump to `v2` in the header
line and the hub only accepts v2. No dual-accept window: enrollment is
an interactive act and callers upgrade on the next signature.
