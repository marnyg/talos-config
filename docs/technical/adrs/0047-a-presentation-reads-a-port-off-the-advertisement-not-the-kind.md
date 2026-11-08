# ADR-0047: A presentation reads a port off the member's advertisement, not its kind

- Status: Proposed 2026-11-07. Evidence: adding the `syncthing`
  gateway facet (ADR-0046) left the desktop's `irohup` refusing
  `198.18.1.32:22000 (syncthing.gw)` with `port is not a facet of the
  member's kind: port 22000 on a gateway` until its daemon was
  rebuilt — the gateway advertised `facets [ingress-http rdp
  syncthing]` and the grant compiled; only the presentation's
  port→facet table was stale. Closes thread `talos-config-m6xc`.
- Date: 2026-11-07
- Related: ADR-0017 (+ 2026-09-18 amendment: kind ≡ facet vocabulary,
  wildcard targets — **amended here for presentations only**),
  ADR-0038 (option C, deferred to `m6xc`; the "found live" staleness
  re-beat that this ADR keeps), ADR-0046 (the facet that forced the
  question), ADR-0025 (the desktop daemon, the peer that must not be
  rebuilt), protocol ADR on `Caveats` as a closed set (why the port is
  encoded in-band)

## Context and Problem Statement

A presentation (irohup's tun, the phone's tunnel, a bridge) turns a
flow to `<name>.mesh.internal:<port>` into a stream to `(member,
facet)`. Since 2026-09-19 the port has been read **in the vocabulary
of the member's kind**: `zone.KindOf` picks the kind off the first
recognised facet the member's reach-me-at advertises, then
`policy.FacetByPort(kind, port)` looks the port up in two compiled-in
tables (`policy.facets`, `policy.facetPorts`).

So every mesh device carries a copy of the facet table, and a new
facet anywhere in the fleet is a rebuild of every device that wants
to reach it — a rebuild for a fact the receiver already states in its
own signed reach-me-at (`actor.Serves` → `cav.facet`). The receiver
knows which facets it serves and on which ports; the peer's table is
a second, drifting copy of that.

`m6xc` asked the sharper question: should presentations read a port
against one kind per member at all, when kind was introduced as a
vocabulary partition for wildcard grant targets, not as a member
type?

## Decision Drivers

- **No peer rebuild for a receiver-side fact.** The receiver is the
  authority on what it serves and where; the plane already carries
  that claim, signed, to every peer on the next beat.
- **No protocol change.** `protocol/` is a vendored module with a
  closed `Caveats` set; a new field is a protocol ADR and a vendorHash
  bump across three modules. Not for this.
- **Old daemons keep working through the transition.** Devices are
  rebuilt one at a time, by hand; the fleet is mixed for weeks. A
  pre-change daemon must read a post-change advertisement as before,
  and a post-change daemon must read a pre-change advertisement.
- **Kind keeps its two real jobs**: the recipe's `node:/gateway:/hub:/
  device:` validation and the wildcard-target partition (ADR-0017), and
  the two-label rule (`<svc>.<member>` needs a gateway). Neither reads
  a port.
- Invariants 1–3 untouched: the advertisement is a location record,
  never authority — the receiver's consent and accept table admit;
  `cert.Authorize` never sees a reach-me-at's `cav.facet` as a grant.

## Considered Options

- **A — keep the table; rebuild peers when a facet lands.** Status
  quo. Rejected: that is the failure this ADR records.
- **B — ship the facet table in the `#bundle`.** The hub compiles the
  table from policy and every beat carries it. Rejected: it makes the
  hub the authority on a receiver-side fact, puts a table on the wire
  that each receiver already states for itself, and still does not
  let a receiver serve a facet the hub's table lacks.
- **C — a new `Caveats` field (`ports map[facet]port`) on the
  reach-me-at.** The clean shape, but a protocol change (closed
  canonical set, vendored module, protocol ADR). Deferred: if the
  in-band encoding ever bites, this is where it goes.
- **D — encode the port in-band in `cav.facet` as `<facet>@<port>`,
  additively beside the bare facet.** `Serves` is `[]string` and the
  protocol accepts any string; the meaning is verb-scoped (a
  reach-me-at's `cav.facet`, never a grant's), as rotation caveats are
  verb-scoped. A receiver advertises both `syncthing` and
  `syncthing@22000`; an old daemon matches bare names for kind and
  skips what it does not recognise; a new daemon reads the port off
  the `@` entries. Chosen.

## Decision Outcome

**D.** `nodeagent.Start` advertises `<facet>` and `<facet>@<port>` for
every facet it serves or forwards (`nodeagent.Advertise`); the port is
the facet's natural port (`policy.FacetPort`) when it has one, else —
for a forward — the port of the forward target, the one fact the
receiver has that no table does. The log line still prints the bare
facet list.

**New zone rule for ports** (`nodeagent/zone.go` `target`): member by
`zone()` as before; facet = the `<facet>@<port>` entry at that port
on the member's live records. If the member advertises **no** `@`
entries at all it is a pre-0047 daemon and the port is read in its
kind's vocabulary (`policy.FacetByPort`) as before. Otherwise a port
nobody advertises is `ErrNotFacet` — which names the member now, not
a kind. `Agent.Target`'s rate-limited re-beat on a miss (ADR-0038)
is unchanged: a member that starts serving still looks like a miss
for one beat.

`KindOf` ignores `@` entries: kind is still read off the bare facets,
still decides the two-label rule and the Android rows, and is no
longer consulted to read a port.

**Amendment to ADR-0017 / 2026-09-18 ruling.** "Kind ≡ facet
vocabulary" stands for grants (disjoint facet names, wildcard
targets, recipe validation). The sentence "a port is read in the
vocabulary of the name's kind" is withdrawn **for presentations**: a
port is read off what the member advertises; the kind reading
survives only as the fallback for records that predate this ADR.

### Consequences

- Good: a facet new to the fleet (`syncthing`, the next one) reaches
  every device on the next beat with no rebuild. The table stays as
  the receivers' convention for natural ports, not as the peers'
  oracle.
- Good: a member may advertise facets from two vocabularies and
  present each at its port — `m6xc`'s question answered the simple way
  (kind was never a member type at presentations; it read one).
- Good: a device whose forward target is not on the natural port
  (`-forward ssh=127.0.0.1:2222`) is presented on the port it actually
  serves, not the port the table assumed.
- Neutral: an `@` entry is an advertisement in the same sense the bare
  one is — it authorizes nothing, and a lying `syncthing@80` only
  misroutes the liar's own callers to the liar's own facet.
- Bad / to watch: the encoding is a string convention on a field the
  protocol treats as opaque; nothing on the protocol side validates
  it. If a second such convention appears, option C is due.
- Bad / to watch: the bare entries are now dead weight for a
  post-0047 fleet. Dropping them is a second cut once no pre-0047
  daemon remains (the fallback in `target` goes with them).
- Ops: receivers advertise the new form once redeployed (gateway
  image, node agents via the served config, devices via their
  daemons); until a receiver is redeployed, peers read it by the
  fallback. No change to recipes, grants, Nickel, or the hub.
