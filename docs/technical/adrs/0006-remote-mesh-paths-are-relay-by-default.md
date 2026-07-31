# ADR-0006: Remote mesh paths are relay-by-default; kill criterion 2 is amended, not fired

- Status: Accepted
- Date: 2026-07-30
- Amends: ADR-0002 — replaces kill criterion 2 ("punch rate") with a
  parity-plus-LAN test. Does not disturb criteria 1, 3, 4.

## Context and Problem Statement

ADR-0002 adopted nebula partly on driver 1, "direct peer paths", and armed
kill criterion 2: if real NAT pairs *mostly fall back to relay*, we have
rebuilt the wg0 topology with more moving parts, so revert to wg0 + a LAN
shortcut.

The phase-1 dogfood measured relay on both remote networks tested. Read
literally, criterion 2 fires. But the criterion carried an implicit causal
claim — that a relay result indicts *our* side (home NAT or nebula config)
— and that claim turned out to be false. This ADR records what was
actually measured and what it means for the decision.

## Measurements (2026-07-29 / 2026-07-30)

NAT mapping behaviour classified by STUN binding requests from one socket
to multiple distinct destination IPs; identical external port ⇒
endpoint-independent ("cone"), differing ⇒ endpoint-dependent
("symmetric").

| Endpoint | NAT behaviour | Punch outcome |
|---|---|---|
| Home (cp1's network) | Endpoint-independent **and** port-preserving (cone) | not the blocker |
| Cellular hotspot | Symmetric CGNAT (tether NAT stacked on carrier CGNAT) | relayed |
| Office Wi-Fi | Symmetric, **random** port allocation (3 dests → 19586 / 51810 / 64036) | relayed |

Supporting detail:

- Nebula did attempt the home WAN directly (`80.212.67.203:4242` present
  in the candidate list) before falling back — `Attempt to relay through
  hosts`, then handshake `from="213.188.219.215:4242 (relayed)"`.
- Config exonerated: `punchy.punch`/`respond` on for both sides, hub punch
  rendezvous unconditional.
- The office run's validity was checked explicitly: Tailscale was up but
  split-tunnel with no exit node (`ExitNodeID`/`ExitNodeIP` empty), and
  `route get` for both cp1's WAN and fly resolved to the physical `en0`.
  Two *earlier* hotspot runs were invalid because wg0 was up and nebula
  used the wireguard tunnel as underlay.

## Decision Drivers

- Hole punching requires at least one side with predictable mapping. Home
  is predictable; both remote networks sampled are not.
- Random (not sequential) port allocation on the office NAT forecloses
  port-prediction workarounds.
- A criterion whose stated cause is disproven should not be executed on
  its symptom alone.

## Considered Options

- **Fire criterion 2 as written** — revert to wg0, build the LAN shortcut.
  Rejected: the shortcut is work already delivered in nebula, and
  reverting also discards mesh DNS, firewall groups, and the phone/TV
  membership story. Most importantly it would revert *to a system that
  also relays remotely* — wg0 hairpins through fly unconditionally — so
  it trades a strict improvement for a regression on LAN.
- **Make the home side unconditionally reachable** — UPnP/NAT-PMP/PCP, or
  a static UDP/4242 forward on the home router. **This would work.** Only
  one side needs to be reachable: once it is, the remote symmetric NAT
  stops mattering, because the remote client initiates outbound and the
  reply rides its own established mapping — no punching, no port
  prediction. Rejected on **invariant 5** (single public entrypoint), not
  on capability. Nebula having no port-mapping support upstream is
  incidental — the mapping is a property of the router, not the overlay.
  Note the mesh already advertises the home WAN as a *lighthouse-discovered*
  candidate, which stays inside the invariant; a deliberate forward would
  not.
- **Port prediction / birthday-paradox spraying** (what Tailscale does for
  hard NATs) — would require patching nebula. Rejected: this is the
  "thousands of lines of connection-state glue" that ADR-0002 rejected
  NetBird for. Rebuilding it in-house is strictly worse than accepting the
  relay.
- **Amend the criterion** (chosen) — restate it as a parity-plus-LAN test,
  which is what the driver actually needs to be worth its complexity.

## Decision Outcome

Nebula stays. Kill criterion 2 is replaced by:

> **Criterion 2 (amended).** Remote paths must be *no worse than wg0*
> (relayed via the hub is parity, not failure), **and** same-LAN paths
> must be direct. If LAN traffic hairpins, the criterion fires.

Measured against the amended form: remote is relay = parity; LAN is direct
at 1.785ms min against a ~20ms hub, with throughput at 70%/92% of the bare
underlay (criterion 3). Nebula is ≥ wg0 everywhere and > wg0 on LAN.

## Consequences

- **Driver 1 survives in LAN-only form.** `desired-state/goals.md` is
  updated so "direct peer paths" no longer implies remote P2P.
- **Fly relay bandwidth becomes load-bearing, not a fallback.** Every
  remote byte — including remote 4K playback — traverses the hub. This is
  a running cost and a throughput ceiling, and it is the thing to watch
  during the rest of the dogfood. It is *also* true of wg0 today, so it is
  not a new cost, but it is now permanent rather than transitional.
- **Criterion 2 no longer gates phase 2.** Remaining gates on stripping
  wg0 are the ≥1wk dogfood and certSANs (phase 2 step 1). Invariant 5's
  dual-overlay exception can close when phase 2 lands; it is no longer
  blocked on a punch verdict.
- The result is a property of the *networks*, not the configuration. A
  future remote network with a cone NAT will punch and go direct with no
  change on our side; nothing needs to be re-decided if that happens.
- Relay-by-default is not a workaround for a NAT-traversal failure — it is
  the network-layer consequence of invariant 5. "The hub is the only
  public surface" and "remote clients reach home directly" are the same
  question with opposite answers. Tailscale gets direct paths here partly
  because it holds no such invariant and will open a router hole when it
  can.

## Revisit when

**Native IPv6 becomes available on the home network.** Checked
2026-07-30: no v6 default route, the ISP does not provide it.

IPv6 is the one route to direct remote paths that does *not* require
amending invariant 5. With no NAT in the path, the firewall pinhole is
opened by ordinary simultaneous outbound traffic — exactly the way
punching is supposed to work — so nothing is statically exposed and no
second public entrypoint is created. That makes it categorically
different from a port forward, which buys the same connectivity by
spending the invariant.

If the ISP enables v6 at home *and* the remote network has it too, re-run
the punch test before assuming relay is still required.

## Confirmation

Verified by the measurements above: NAT classification at both ends, a
capture-verified relay on the hotspot pair, route-table confirmation that
the office run egressed the physical interface, and the direct-LAN latency
and throughput numbers recorded in
[`../deployed-state.md`](../deployed-state.md).
