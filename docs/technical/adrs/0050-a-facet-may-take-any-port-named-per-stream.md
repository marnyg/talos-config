# ADR-0050: A facet may take any port; the caller names it per stream

- Status: Proposed 2026-11-21. Built and tested (`talos-config-vjky`);
  the recipe row is on the tip, so the hub compiles the `tcp` grant for
  `admins` on its next poll. **Nothing is reachable yet**: no device
  forwards the facet until its daemon is rebuilt from this revision and
  started with `-forward 'tcp=<host>:*'`. The owner asked for the
  capability ("add the wildcard", admins only, opt-in receivers); the
  facet name, the `@*` encoding, the nominal port and the 443 rule
  below are this ADR's proposals and have not been reviewed.
- Date: 2026-11-21
- Related: ADR-0016 (identity-native mesh; the kill criterion this must
  not trip), ADR-0017 (+ 2026-09-18: kind ≡ facet vocabulary, wildcard
  targets), ADR-0038 (a device that serves is a receiver; `ssh` is the
  shape this generalizes), ADR-0047 (`<facet>@<port>` in the
  advertisement — **extended here**, see "to watch"), ADR-0048 (a
  receiver accepts exactly what it forwards; a facet is a recipe row),
  ADR-0033 (the :443 fiction).

## Context and Problem Statement

The mesh reaches named facets, each a fixed `facet → host:port` on the
receiver. Reaching a new port on one's own machine — a dev server, a
database, a one-off listener — is a recipe row, a `-forward`, and a
daemon restart, per port. The owner wants what an IP overlay gives for
free on the owner's own devices: `<name>.mesh.internal:<any port>`.

The overlay itself stays ruled out (ADR-0016): no fleet-coordinated
addresses, no packets. The question is how much of "any port" the
stream plane can carry without a port entering a grant.

## Decision Drivers

- Invariants 1–3 untouched: `cert.Authorize` still admits a signed
  grant for a facet the receiver consented to, and nothing else.
  A port never enters a cert, an ALPN or the recipe's rules.
- No protocol change (`protocol/` is vendored; closed `Caveats` set).
- No hub or gateway rebuild to turn it on (ADR-0048's point): the
  deployed hub must parse the recipe that names the facet.
- Opt-in per receiver, by the receiver: ADR-0038's rule that a
  device's consent is its opt-in.
- Mixed fleet: a daemon from before this ADR must read a member that
  takes any port as it reads any member with a facet it does not know.

## Considered Options

- **A — one facet per port, as today.** Rejected: it is the friction.
- **B — packets (QUIC datagrams, an L3 path).** UDP and ICMP too, but
  it needs addresses that mean the same thing on two machines — the
  design ADR-0016 refuted. Not for this.
- **C — the port in the grant** (`cav.port`, ranges). Port-level
  policy, but a protocol change, and ports in certs is the v2 model
  ADR-0017 left.
- **D — the port in the ALPN** (`talos-mesh/tcp-8080/v1`). No new
  framing, but the receiver must bind 65 535 ALPNs or the accept table
  becomes a pattern, the port shows in the ClientHello, and the pool
  holds a connection per port.
- **E — a facet with no port of its own; the caller names one at the
  head of each stream.** One ALPN, one grant, one pooled connection per
  member; the port is payload framing between two parties that have
  already authorized. Chosen.

## Decision Outcome

**E.**

**Receiver.** A forward whose target is `<host>:*` is a facet that
takes any port (`nodeagent.AnyPort`). It is accepted and consented
like any facet (`receiverTable`: exactly `Forward ∪ Serve`), has no
port to present, and is advertised as `<facet>` and `<facet>@*`
(`AdvertiseAny`). Each admitted stream begins with a two-byte
big-endian port (`WritePort`/`ReadPort`; 0 is refused); the receiver
splices the rest to `<host>:<port>` and logs one line per stream
(`tcp: "desk" → 127.0.0.1:8080`) — the only record of which port was
reached, since the admission names the facet alone. The mechanism is
kind-agnostic; who may use it is a recipe row.

**Presentation.** `nodeagent.Route(name, port)` is `Target` plus the
fallback: a facet the member advertises at exactly that port wins;
otherwise a facet it advertises as `<facet>@*` carries the flow with
`Route.Port` set, and `meshtun.Pool.OpenRoute` writes the header. So a
device forwarding both `ssh=127.0.0.1:22` and `tcp=127.0.0.1:*` is
reached on :22 under the `ssh` grant and everywhere else under `tcp`.
`Target` stays the exact reading and never answers with an any-port
facet. A member that advertises only `<facet>@*` counts as advertising
ports, so it is not read by the pre-0047 kind fallback. Bridges take
`<member>/<facet>@<port>=<listen>`.

**:443.** On a name with an :80 facet the TLS fiction stands (ADR-0033).
On a name with no :80 facet that takes any port, :443 is the member's
own port, spliced raw — its TLS, its certificate (`Tun.rawHTTPS`).

**Recipe.** One facet, one row:

    facets:  - {name: tcp, kind: device, port: 1}
    device:  - {facet: tcp, group: admins}

`port: 1` is nominal (IANA's tcpmux): the vocabulary's shape wants a
number 1–65535 and the deployed hub validates that, so a truthful
"no port" would have been a hub rebuild and an unseal before the row
could land. No presentation reads it — the receiver does not advertise
`tcp@1`, and a flow to :1 on such a member routes as any other port.

**Who reaches what.** Every member whose cert carries `admins` → every
TCP port on `<host>` of every device started with
`-forward 'tcp=<host>:*'`. Not `media`, not `machines` (nodes, the
gateway), not the hub. Devices that do not pass the flag, all nodes,
the gateway and the hub are exactly as before. The grant's target is
the wildcard (ADR-0017), so the recipe cannot say "this device but not
that one"; the per-device choice is the device's flag.

### Consequences

- Good: any TCP service on an owner device, by name and its real port,
  from every owner device; no row, no restart per port. Android gets
  it through the same `meshtun` flow.
- Good: nothing new in certs, ALPNs or the hub; turning it off is
  dropping the flag (consent lapses with the process) or the row.
- Bad, by construction: **the gate is the group, not the port.** Any
  `admins` device reaches every port on `<host>`. With `127.0.0.1`
  that includes services that listen on loopback alone and trust
  whoever connects (a database with local `trust` auth, a debug
  endpoint, an admin UI); they see the caller as local, and there is
  no second gate unless the service brings one. `ssh`/`rdp`/`syncthing`
  each had one. A stolen `admins` device key is worth that much more
  until its cert expires (revocation latency ≥ runway).
- Bad / to watch: this is the **second in-band convention on
  `cav.facet`** (`@<port>`, now `@*`), the trigger ADR-0047 named for
  its option C (a `Caveats` field on the reach-me-at). Taken as an
  extension of the first rather than a new one — same separator, same
  verb scope, skipped by older daemons — but the next one should not
  be argued the same way.
- Bad / to watch: the nominal `port: 1` is a number that means nothing.
  The honest shape is an optional port (absent ⇒ any); it costs a hub
  deploy ordered before the recipe edit. Do it with the next hub image.
- Neutral: TCP only. UDP, ICMP, subnet routes and exit nodes are
  option B and stay out.
- Neutral: a bridge cannot check `@<port>` against the advertisement
  before the first beat; a bridge spec that disagrees with the
  receiver's forward is a stream the receiver drops.
- Not done: nodes and the gateway. A node taking any port exposes
  kubelet, etcd and every NodePort to `admins` and needs a fleet image;
  "any port" on the gateway means a pod's loopback. Each would be its
  own facet name (kind ≡ facet vocabulary) and its own decision.

### Confirmation

`config-server`: `TestRouteAnyPort`, `TestPortHeader`,
`TestReceiverTableAnyPort`, the bridge/forward flag tests, and the
tagged `TestNodeAgentEndToEnd` section — an `admins` desk reaches two
ports on one admitted connection, a dead port ends its stream without
costing the connection, the gateway (`media`) and the node
(`machines`) are refused at the preamble. Nickel `check.sh` passes
unedited. Live confirmation is still owed: two rebuilt devices, one
with the flag, and `curl <name>.mesh.internal:<port>` between them.
Invalidated if a use appears that needs per-port or per-device policy
in the grant (option C), or a non-TCP protocol (option B).
