# ADR-0026: In-cluster gateway injects the verified device identity; the gateway zone replaces per-machine service names

- Status: Accepted
- Date: 2026-09-20
- Revises: ADR-0007 (cert-group + source-address inference), ADR-0009
  (service exposure over nebula-native ingress, `<svc>.<member>` names)

## Context and Problem Statement

Mesh v3 (ADR-0016/0017) dials members by key and leaves IP as a
device-local fiction, so ADR-0007's authentication — nebula cert group
plus derived source address — has nothing to read at ingress: a pod
sees a pod-network source, and the member's identity is a cert chain
presented on the stream, not an address. ADR-0009 named services
`<svc>.cp1.mesh.internal` because ingress-nginx happened to run on
cp1; under v3 a service is a facet on some actor, and that actor is
not a machine. What terminates identity streams for Kubernetes
Services, how does the verified identity reach an app, and what are
services called?

## Decision Drivers

- Invariant 2, "git is compiler input, never verifier input": the
  terminating actor decides from the presented bundle alone.
- Invariant 1: a member's key is minted where it lives; the gateway
  must survive pod restarts without a wallet act.
- Decision `359.5`: one gateway pod, node agents only for
  apid/kube-api. Decision `d3z3`: a migrated consumer cuts its nebula
  path. `1gv`: the identity header must be unforgeable from the LAN.
- The app layer stays on the SIWE→OIDC bridge (ADR-0010): the header
  complements SIWE, it does not replace it (grill 2026-09-03).
- The presentation (irohup's tun, later the Android app) must read a
  name's kind without a registry.

## Considered Options

### Gateway placement and identity

- **Hub-side gateway (hubkey)** — the architecture sketch's first
  shape. Cons: every Service call hairpins through fly; the hub's
  ephemeral key would need unattended membership; refuted by `359.5`.
- **Per-node agents also serving ingress** — cons: services named by
  machine again; the node's key answers for workloads it does not own.
- **In-cluster member pod, durable key on a volume (chosen)** — the
  node agent runtime under `Kind gateway`; key + Kit on a Longhorn
  PVC, enrolled once by the headless device flow (`nodeagent.
  EnrollDevice`, the `4ps` mechanism), ordinary member thereafter.

### Header injection point

- **Gateway forwards to Services directly and re-implements
  routing/auth_request** — rebuilds ingress-nginx; ruled out.
- **Gateway terminates HTTP and reverse-proxies to ingress-nginx,
  Host untouched, strips inbound `X-Mesh-*`, sets them from the
  admitted identity (chosen)** — nginx keeps routing by Host and
  gating with `auth_request`; the identity is `cert.Authorize`'s
  `Identity` (member cert only), once per connection, connections
  bounded to 1 h (`ConnMaxAge`) so expiry has a ceiling.
- Forgeability from the LAN (`1gv`): while ingress-nginx is
  hostNetwork, nginx honours `X-Mesh-*` only from the pod network
  (`geo` on the peer address + `proxy-set-headers`), and the gateway
  dials its **node-local** controller by host address — a ClusterIP
  hop to the other node is flannel-masqueraded to the node IP and the
  gate blanks the headers (found live). Ends when hostNetwork goes.

### How a presentation knows a name's kind

- **A fixed gateway name known to the client** — one constant, but a
  registry in miniature.
- **Depth alone (`<svc>.<m>` ⇒ gateway)** — shadows nebula's
  `sonarr.cp1` during the dual plane.
- **The member advertises the facets it serves in its reach-me-at
  (chosen)** — `actor.Serves → cav.facet` beside the endpoints; the
  zone rule (`nodeagent.Zone`) reads one label as a member in its
  advertised kind and `<svc>.<member>` only when the member advertises
  a gateway facet. An advertisement, never authority (ALPN routes,
  Authorize decides). A record advertising nothing reads as a node
  (agents ≤ 0.1.3; a device that serves nothing).

## Decision Outcome

Chosen: **in-cluster member gateway `gw`, HTTP-terminating reverse
proxy with verified identity headers, facets advertised on the
reach-me-at, services named `<svc>.gw.mesh.internal`.** Every Ingress
host and the SSO issuer (`auth.gw`) moved at once (`5qh9`): the
owner's desktop had already lost its nebula plane (P2.0–P2.2), so the
Jackett-first step could not keep SSO on `.cp1`. `jellyfin.cp1` stays
as a second host for the nebula TV until P2.4.

### Consequences

- ADR-0007's mechanism is retired on the identity plane: identity is
  per stream, from the cert chain, not inferred from an address.
  ADR-0009's naming becomes per-gateway; "exposing a service is an
  Ingress in git" still holds, now under the `gw` zone.
- Apps may read `X-Mesh-Node/Name/Groups`; none is required to trust
  them for a session (SIWE→OIDC stays the app-layer seam).
- The gateway is a single pod with a single key: its PVC is the
  membership; losing it means one wallet act (device flow), nothing
  else. Recreate strategy: one holder at a time.
- Natural ports gain `ingress-http: 80`, `jellyfin: 8096`; ports are
  read per kind (`hub:80` and `<svc>.gw:80` differ).
- Interim `1gv` gate and the node-local dial are dual-plane scaffolding;
  the rest-of-ingress move off hostNetwork deletes both.

### Confirmation

A backend behind `whoami.gw.mesh.internal` saw `X-Mesh-Name:
marius-mac`, `X-Mesh-Groups: admins` over the identity plane and no
`X-Mesh-*` at all from a LAN request carrying forged ones; Jackett and
ArgoCD sign in through `oauth2.gw`/`auth.gw`; the gateway pod
restarted with its key and Kit from the PVC without re-enrolling.
Invalidated if a second gateway (or per-node ingress) is ever needed
for availability — then the "one pod, one key" shape is wrong.
