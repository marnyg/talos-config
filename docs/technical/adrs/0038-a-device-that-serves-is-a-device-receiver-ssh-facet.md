# ADR-0038: A device that serves is a receiver of kind `device`; its first facet is `ssh`

- Status: Proposed — code, contract and recipe row landed 2026-10-17
  (`talos-config-thz2`); awaits the hub redeploy + unseal that compiles
  the row and a live `ssh nixos.mesh.internal` from the Mac.
- Date: 2026-10-17
- Related: ADR-0017 (+ 2026-09-18 amendment: kind ≡ facet vocabulary,
  wildcard targets), ADR-0015 (git cannot enumerate receiver keys),
  ADR-0025 (the desktop daemon that now also serves), ADR-0032
  (where capability discipline ends — the second gate); the `rdp`
  facet (raw-TCP-plus-service-login precedent); thread
  `talos-config-m6xc` (one kind per member at presentations); spike
  `talos-config-7ymy` (person binding, if sshd's principal is ever the
  mesh identity)

## Context and Problem Statement

The owner wants to ssh to the ordinary machines on the mesh — the Mac
and the NixOS box — the way `talosctl` already reaches the Talos nodes
through `apid`. Talos nodes are out of scope: they have no sshd and
`apid`/`kube-api` are their control surfaces.

The domain model said a device *only initiates*: "a device has no
facets, so it is not a kind" (`policy.go`, glossary "Recipe"). To be
ssh'd *into*, a device must serve a facet, and every facet lives in
exactly one receiver kind's vocabulary — so there was no legal place
for `ssh`.

The owner's first framing was "this is an ACL problem — groups and
principals, not kinds". That framing is right about the authorization
and this ADR records why the change is nevertheless a vocabulary
change.

## Decision Drivers

- The authorization already **is** groups and principals: a recipe row
  `{facet, host|group}` compiles to an `invoke` grant addressed to a
  principal or a group; the receiver's consent (the facets it chose to
  serve) is where the grant is honored. Nothing new is needed there.
- Kind is **not** an access-control concept. It partitions facet names
  so a grant's target can be `"*"` (ADR-0015: git cannot name receiver
  keys), groups the recipe for validation, and tells a presentation how
  to read a port (`nixos.mesh.internal:22`). The ssh case needs a home
  for the name `ssh`; that is all the kind does.
- Receiver-side ACL ("only the laptop may ssh to the box") is not
  expressible in grants and should not be: it is sshd's
  `authorized_keys`, the same split as `rdp` (mesh grant = who may
  knock; the service's login = who may enter).
- Invariants 1–3 untouched: device keys stay on the device, the
  receiver decides offline from the certs presented, no registry.

## Considered Options

- **A — `ssh` as a `node` facet.** Mechanically works (the NixOS box
  would run with `-forward ssh=…` and read as a node). Rejected: it
  types the box as a *machine* (git-enumerated, boot-token enrolled),
  which it is not, and by vocabulary a Talos node could serve ssh.
- **B — a new receiver kind `device` with the facet `ssh`.** The
  minimal bookkeeping: one vocabulary entry, one recipe scope. A
  device that serves nothing is unchanged (advertises nothing, reads
  as a node, refuses at the ALPN). Chosen.
- **C — drop "one kind per member" at presentations and read a port
  off the advertised facets.** Removes the sense in which a kind looks
  like a member type at all. A real simplification, but it reopens the
  2026-09-18 ruling and is independent of ssh — filed as `m6xc`, not
  decided here.
- **D — sshd principal = mesh identity (`AuthorizedKeysCommand` fed by
  the member cert, as the gateway feeds `X-Mesh-Token`).** Collides
  with the open person-binding spike `7ymy`. Not now.

## Decision Outcome

**B.** Kind `device` joins `node`, `gateway`, `hub` with the
vocabulary `{ssh}`, natural port 22 (`config-server/policy`,
`verification/nickel/mesh-policy-v3.ncl`, the recipe's `device:`
scope). The recipe grants `{facet: ssh, group: admins}`: any admin
member may *knock*; the receiver's consent decides *where*, sshd
decides *who enters*.

`irohup -forward <facet>=<host:port>` (device facets only) makes the
daemon a receiver: it advertises the facet in its `reach-me-at`, signs
consent for exactly it, and splices admitted streams to the loopback
target. Without `-forward` nothing changes. `-bridge` now accepts any
facet with a kind, so a device without a tun reaches another's sshd
by `-bridge <name>/ssh=127.0.0.1:2222`.

Presentations need no change: `zone.KindOf` reads `device` off the
advertised `ssh`, so `ssh <name>.mesh.internal` through a tun (or the
phone's tunnel) maps `:22 → (name, ssh)`.

### Consequences

- Good: the ssh path is one recipe row plus one flag per serving
  device; the glossary now states explicitly that kind is vocabulary,
  not ACL, and that the ACL is the grant + consent.
- Good: `ssh` is a device vocabulary word — a node or gateway cannot
  advertise it.
- Neutral: an ssh session is a facet connection with no `ConnMaxAge`
  (as `apid`); it ends when the connection does, authorization ran once
  at connect. The gateway's 1 h bound is for HTTP, not shells.
- Bad / to watch: a member advertising facets from two vocabularies is
  still read as one kind (`m6xc`). The first such member will be a
  device serving both `ssh` and something else.
- Ops: sshd on the serving device must listen on loopback (default);
  the Mac's launchd plist and the box's systemd unit gain
  `-forward ssh=127.0.0.1:22`; a hub redeploy + unseal compiles the
  row into every admin's next `#bundle`.
