# ADR-0013: Custom Android app as the TV mesh client

- Status: Proposed _(the Confirmation below — the parents' TV playing
  over the relayed path, a renewal from the couch — is still unmet;
  `talos-config-4te`)_
- Date: 2026-08-15
- Revised by: ADR-0016 (in force 2026-09-21, Mesh v3 Phase 4) — **app
  internals only**: the nebula AAR is replaced by `config-server/mobile`
  (the `nodeagent` runtime + `meshtun` on the `VpnService` fd, fake-IP
  `198.18/15` split routes, split DNS) through the same gomobile
  pipeline (`android/build-aar.sh`); enrollment is the device flow
  through Go, minting an iroh NodeId instead of a nebula key (P2.4,
  `359.9.4`, verified on the owner's phone 2026-09-20). Option C and
  its drivers stand. Two Consequences below are overtaken: `GET /hosts`
  was deleted with the policy overlay (ADR-0014 revision `ri3b`) — the
  app reads the name map from the identity plane; and "no DNS is pushed
  to the tun" is resolved by split DNS — the app answers the mesh zone
  itself and forwards everything else to the underlay, so
  `jellyfin.gw.mesh.internal` Host-header routing works without a hub
  upstream-forwarding mode.

## Context and Problem Statement

A remote TV (parents' house) needs Jellyfin over the mesh — the
build-gate deferred since 2026-07-30 (task `2e1bef85`). The device
cannot sign wallet messages, runs Android TV with a D-pad, and must be
onboardable and re-enrollable (90-day certs, ADR-0012) by an operator
holding only a phone with MetaMask. What client software joins a TV to
the mesh?

## Decision Drivers

- Invariant 1 / ADR-0012: device-born keys; only the pubkey travels;
  membership = one wallet signature, re-establishable any time.
- TV UX reality: D-pad navigation, no camera, no local wallet.
- The hub's RFC 8628 mesh-enroll flow already exists server-side and
  was designed for exactly this ("headless appliance, TV").
- Distribution must not depend on a store account (invariant 3 spirit:
  no hosted gatekeeper in the path).
- Owner appetite: a Tailscale-style app — login + list of reachable
  devices, nothing more.

## Considered Options

### Option A: Sideload stock Mobile Nebula

Use DefinedNet's app on the TV, importing the hub-issued yaml (the
verified phone path).

- Pros: zero code to own; already proven on the phone (2026-08-14).
- Cons: Flutter buttons ignore D-pad clicks on Google TV
  (DefinedNet/mobile_nebula#148); no camera for its QR import; no
  leanback intent; config import is manual field-entry clunk repeated
  every 90 days. Not identity-aware: no wallet-anchored enrollment UX.

### Option B: No client — LAN-direct Jellyfin only

The pre-2026-08 position: home TV stays off the mesh.

- Pros: nothing to build or maintain.
- Cons: does not serve the remote-TV use case at all, which is the
  requirement that fired the gate.

### Option C: Custom Kotlin app over a gomobile bind (chosen)

Two-screen app: RFC 8628 enroll (on-device keygen, hub-rendered QR,
poll, local key-splice) and a VpnService toggle + `/hosts` device list.
Go owns crypto/protocol (`config-server/mobile`, sharing `devkey` with
nebup); nebula runs on the tun fd via upstream
`overlay.NewFdDeviceFromConfig` — no fork. One APK serves TV (leanback)
and phone (touch).

- Pros: identity-aware (third ADR-0012 client with identical key
  semantics, enforced by shared code); D-pad-first UI; enrollment and
  renewal are scan-and-sign; repo is the distribution channel.
- Cons: we own an Android app + gomobile toolchain; CI is the only
  builder; ~94 MB APK (4 ABIs) until trimmed.

## Decision Outcome

Chosen: **Option C**, because the remote-TV requirement is real, Option
A fails basic TV interaction, and the hub's device-flow endpoints plus
ADR-0012's device-born-key model already define the client's entire
contract — the app is a thin, mostly-generated shell around them.

### Consequences

- The mesh HTTP surface gained `GET /hosts` (device list, admins+media
  gated); tcp/80 on the hub firewall now admits media-group certs.
- **No DNS is pushed to the tun**: Android routes all device DNS to a
  VPN resolver and the hub's resolver answers only the mesh zone —
  services are reached by overlay IP from `/hosts`. If ingress-name
  addressing becomes necessary (Jellyfin Host-header routing), the hub
  needs an upstream-forwarding mode or the app needs Android-13+ split
  DNS. Deliberately unresolved.
- **Debug-signed, rolling release** (`android-latest` asset URL):
  sideload-friendly, but switching to a release keystore later forces
  uninstall + re-enroll on every device — cheap under ADR-0012 (one
  signature; same name keeps the same address), but a ceremony.
- The repo owns an Android/gradle/gomobile toolchain that only CI
  exercises; x/mobile is pinned in go.mod (`tools.go`).

### Confirmation

The parents' TV plays Jellyfin over the relayed mesh path with
acceptable UX, and a 90-day renewal is completed from the couch
(re-enroll from the app, one phone signature). Invalidated if relay
throughput can't carry playback (revisit ADR-0006's remote-path
options) or if D-pad enrollment proves unusable in the field.
