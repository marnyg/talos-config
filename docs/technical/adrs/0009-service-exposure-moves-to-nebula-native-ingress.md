# ADR-0009: Service exposure moves to nebula-native ingress with scoped DNS names

- Status: Accepted
- Date: 2026-07-31

## Context and Problem Statement

Cluster services (Jellyfin, the arr stack) were exposed through
tailscale ingresses — a hosted identity account sitting in the access
path, in tension with invariant 3 ("roots of trust are keys the owner
holds, never accounts someone hosts"). Separately, reaching services
required remembering ports, and the SSO plan (sketch `162c630d`) needs
an ingress layer with forward-auth support, which the tailscale
ingressClass cannot provide. The question: how do services get names
and reachability over the mesh, without a hosted account and without
per-service ceremony?

## Decision Drivers

- Invariant 3: no hosted account in the access path.
- Invariant 5: no new public surface — services stay mesh/LAN-only.
- Mesh DNS's core property: the zone is a pure function of
  (git, master); resolution must not depend on runtime state.
- Adding a service should be cheap — ideally one git change, no hub
  deploy, no wallet unseal (every deploy re-seals, thread `19a4c316`).
- The SSO layer needs nginx-class forward-auth (`auth_request`).

## Considered Options

### Option A: Status quo — tailscale ingresses

- Pros: worked; TLS and names for free.
- Cons: hosted account in the access path (invariant 3); no
  forward-auth for the SSO plan; a second overlay beside nebula.

### Option B: Flat declared aliases (built, then discarded)

Machine meta.yaml declares `aliases: [jellyfin, …]`; hub DNS serves
them beside member labels (kept out of the member zone so /status
never renders vhosts as machines).

- Pros: explicit inventory of exposed services in git; collision
  checking against member names at unseal.
- Cons: adding a service costs meta.yaml edit → fly deploy → wallet
  unseal, and duplicates what the Ingress already declares. Built
  first, replaced by Option E in the same session.

### Option C: In-cluster DNS (external-dns / CoreDNS zone)

- Pros: k8s-native; names appear when Ingresses do.
- Cons: breaks "the zone is a pure function of (git, master)" — names
  die when the workload is down, exactly when you may want them; needs
  a second resolver rule on every device or hub→cluster query
  forwarding at runtime; no view of the member set, so nothing catches
  an Ingress claiming `laptop.mesh.internal`.

### Option D: Pod-level mesh membership (nebula operator)

- Pros: per-service nebula firewall groups (TV reaches jellyfin's mesh
  IP but cannot handshake sonarr's).
- Cons: no such operator exists (the GitHub `nebula-operator` is
  NebulaGraph's; Defined Networking's agent is a hosted control
  plane — invariant 3). In principle it still fights invariants 1
  and 6: an operator's job is runtime cert issuance, and pod churn
  cannot be human-ratified. The authorization capability relocates to
  the SSO layer instead.

### Option E: Scoped service names + ingress-nginx (chosen)

Hub DNS resolves any `<service>.<member>.mesh.internal` (one level,
members only, exact member match wins) to the member's address.
ingress-nginx (DaemonSet, hostNetwork :80 on the single node) routes
by Host header. Plain HTTP by decision — nebula encrypts the wire;
HTTPS arrives with the wallet-derived CA (deferred task).

- Pros: exposing a service is purely an Ingress in git (ArgoCD syncs;
  no deploy, no unseal); the namespace is partitioned by depth, so a
  service name structurally cannot collide with a member label; the
  Ingress `host:` field is the single inventory; `auth_request` is
  available for the SSO layer; one wildcard cert per machine when
  HTTPS lands.
- Cons: names are machine-coupled — if a service moves nodes its URL
  (bookmarks, OIDC redirect URIs) changes; any name under a member
  resolves whether or not an Ingress exists (nginx 404s it); plain
  HTTP means LAN-direct access rides unencrypted on the LAN until the
  CA lands.

## Decision Outcome

Chosen: **Option E**. One resolution rule replaces declared data: the
zone stays a pure function of (git, master), the hub is untouched per
service, and the tailscale account leaves the access path entirely.
Access control moves to the SIWE SSO layer (sketch `162c630d`), which
this ingress is the substrate for.

### Consequences

- Services are `http://<name>.cp1.mesh.internal/` — no ports.
- `k8s/apps/tailscale/` deleted; the pruned Application may orphan
  helm resources (no finalizer) — `kubectl delete ns tailscale` on
  first rollout if so.
- One hub deploy + unseal needed once (the dnsRespond rule), never
  again per service.
- The nebula firewall sees only member→cp1:80; per-service
  authorization is the SSO layer's job from here on.

### Confirmation

Right when a new service appears by merging an Ingress alone and
resolves over the mesh. Invalidated if a second machine hosts services
whose URLs must survive moves (revisit flat aliases then), or if
mesh-layer per-service authorization ever becomes a requirement.
