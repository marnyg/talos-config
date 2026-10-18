# ADR-0042: Pages go to a self-hosted ntfy over the mesh

- Status: Accepted 2026-10-18 (owner). Built the same day; live
  confirmation pending (see Confirmation).
- Date: 2026-10-18
- Related: ADR-0041 (the stack whose alerts these are), ADR-0027
  ("push alerting is still missing … a push channel, if ever wanted,
  should hang off this same observation"; and "an alert sink is an
  account someone else hosts" as a driver), ADR-0032 (the gate as the
  login), ADR-0033 (https on the mesh), invariant 3 (roots of trust),
  invariant 5 (single public entrypoint), `relay.go` ("a sealed hub
  still relays"); task `talos-config-88uq`

## Context and Problem Statement

Gatus (`status.gw.mesh.internal`) probes 23 rows and Alertmanager
evaluates the chart's rules, and neither could reach the owner: Gatus
had no alerting block and Alertmanager routed everything to `null`
with the note "nothing public may carry a page and no in-mesh notifier
exists". The row that matters most is `hub (unsealed)` — a sealed hub
is the runway (6 d grants, 30 d membership) running out, and the
owner finds out today by opening `/status`. Which channel carries a
page, and what does it cost against the invariants?

## Decision Drivers

- Invariant 5: no new public surface, nothing dialled from outside.
- Invariant 3 in spirit: every other service here is self-hosted and
  behind the gate; ADR-0027 already declined a hosted sink once. A
  page carries no authority, so a hosted sink would not *break* an
  invariant — but it would be the first hosted account in the
  operating path.
- The sealed-hub page must be deliverable **while the hub is sealed**,
  remotely. Sealed ≠ down: the relay keeps running across seals.
- Native support in the three publishers (Gatus, Alertmanager,
  Grafana) so the wiring is config, not code.
- Phone-first: the owner's Android phone and Mac are both mesh members.

## Considered Options

### Option A: ntfy self-hosted in-cluster, subscribed over the mesh (chosen)

`k8s/apps/ntfy`, behind the admins gate at `ntfy.gw.mesh.internal`,
no auth of its own, no upstream to ntfy.sh (no Firebase), 24 h message
cache on a 1Gi Longhorn volume. Gatus publishes natively; Alertmanager
through the `ntfy-alertmanager` shim (a small Go translator, single mode,
label → priority). The Android app keeps its own connection through
the device's tunnel.

- Pros: no hosted account; nothing public; the same proxy-to-X posture
  as Grafana. The sealed-hub page is deliverable: device → relay →
  gateway → ntfy, and the relay survives sealing. A phone that was off
  the mesh catches up from the cache.
- Cons: a hub that is fully *down* cannot be paged remotely — but that
  case is self-announcing (the whole mesh is gone from the phone) and
  on the LAN the direct path still delivers. Delivery rides a
  foreground service rather than FCM; Android may defer it under
  aggressive battery policy. One more shim pod.

### Option B: ntfy.sh with a random topic

- Pros: no account either; works even with the hub down.
- Cons: every alert body leaves the mesh to a third party; the topic
  name is the only secret; Firebase in the path.

### Option C: Telegram bot

- Pros: native everywhere, the most reliable phone push, works with
  the hub down.
- Cons: a hosted account (owner's Telegram + a bot token in a
  SealedSecret) in the operating path; alert bodies off-mesh.

### Option D: email via an SMTP relay

- Cons: a hosted relay (self-hosted mail from a home IP does not
  deliver); not a page — latency, filters.

### Option E: the hub pushes over the identity plane

A `notify` facet on the device receiver (ADR-0038's shape), the hub as
publisher: the party already outside the failure domain pages the
phone directly.

- Pros: the only option that pages when the hub is up and the cluster
  is not, with no cluster hop at all.
- Cons: protocol work (a device-side facet, a hub-side publisher), and
  the cluster's own alerts would still need a path to the hub. Not v0;
  the natural successor if Option A's "hub down" gap ever bites.

## Decision Outcome

Chosen: **Option A**. The deciding fact is `relay.go`: a sealed hub
still relays, so the one alert that must page is deliverable in-mesh
without buying a hosted channel for it. Policy as config:

- Gatus: `alerting.ntfy` → `http://ntfy.ntfy.svc`, topic `alerts`,
  3 misses before a page, 2 hits before the all-clear, all-clear sent;
  priority 4, the `external` group (the hub row) priority 5.
- Alertmanager: receiver `ntfy` → `ntfy-alertmanager.monitoring.svc`,
  `Watchdog`/`InfoInhibitor` to `null`, the upstream inhibit rules,
  12 h repeat; the shim maps `severity` critical/warning/info to
  priority 5/4/2 and resolved to 2.
- No SealedSecret: there is no token. The gate is the subscriber's
  login; publishers are trusted by reachability (the known pod-side
  gap of every gated Service, ADR-0041).

### Consequences

- A new directory and app-of-apps line (`ntfy`); the monitoring
  directory gains the shim. Both are plain Deployments; no ordering.
- One topic, `alerts`. A second publisher (Forgejo, Home Assistant,
  the hub one day) is a POST to the Service and, if it wants its own
  subscription, its own topic — nothing to register.
- The Watchdog heartbeat still has no receiver outside the failure
  domain; the hub (ADR-0027) is where one would go.
- Grafana's own alerting is not wired; Prometheus rules → Alertmanager
  is the one alert path. Add a Grafana contact point (webhook to the
  shim is not the right shape; ntfy's JSON publish is) only if a
  Loki-based alert ever needs it.
- The device must trust its meshtls root for `https://ntfy.gw…`
  (ADR-0033); `http://ntfy.gw…` is served beside it for an app that
  will not use a user CA.

### Confirmation

- Correct when: scaling a probed Deployment to 0 pages the phone
  within ~4 min and the all-clear follows the scale-up; a sealed hub
  (`/sealed` 503) pages at priority 5 — to be observed at the next
  deploy, which seals the hub by design; a Prometheus alert
  (`KubePodCrashLooping` from a deliberate bad image is the cheapest)
  arrives through the shim with `rotating_light`/`warning` tags.
- Revisit if: the "hub down, owner remote" gap actually costs a day
  (then Option E, or Telegram for the hub row only); Android delivery
  proves unreliable without FCM; ntfy grows auth needs (per-person
  topics) — then it is a person-gate app, not a group-gate one.
