# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-20 (ninth session) — **P2.3 live; `359.9.3` + `5qh9` done.**

- The in-cluster gateway `gw` (`config-server/gateway`, `cmd/gateway`,
  `k8s/apps/gateway`, image `ghcr.io/marnyg/gateway:ee8155b`) is the
  node agent runtime under `Kind gateway`: `ingress-http` terminated
  in-process (`facethttp`, shared with hub-http) as a reverse proxy to
  the node-local ingress-nginx with `X-Mesh-Node/Name/Groups`;
  `jellyfin` a raw splice; `ConnMaxAge` 1 h. Enrolled once by the
  headless device flow (`nodeagent.EnrollDevice`, the `4ps` mechanism);
  key + Kit on a Longhorn PVC survived a pod restart. ADR-0026.
- **reach-me-at advertises served facets** (`actor.Serves` →
  `cav.facet`); the presentation's zone rule (`nodeagent.Zone`) reads
  `<svc>.<member>` only on a gateway. Natural ports `ingress-http` 80,
  `jellyfin` 8096, read per kind.
- **All ingress moved to `*.gw.mesh.internal`** incl. the SSO issuer
  `auth.gw` (swarm worker `ingress-gw`, merged `d39eaf9`): the Mac has
  no nebula plane any more, so `.cp1` was already dead there.
  `jellyfin.cp1` kept for the nebula TV until P2.4.
- `1gv` interim: nginx honours `X-Mesh-*` only from the pod CIDR;
  verified with a whoami backend (LAN-forged headers blanked). A
  ClusterIP hop to the other node gets flannel-masqueraded → the
  gateway dials `$(HOST_IP):80` instead (exploration log).
- Invariant 2 amended: the gateway is a member, not a hub actor.

## Loose threads

- **Nodes still run p0agent 0.1.3**, whose reach-me-at advertises
  nothing; `nodeagent.KindOf` reads that as a node (transitional
  fallback — keep until both nodes run an agent built from ≥ `3e9fdef`,
  then it is merely the "device that serves nothing" reading).
- The hub is at `98acac7`; it does not need the new code for P2.3
  (hub-http kind comes from `HubName`), but the next deploy picks up
  the `facethttp` refactor — watch `TestHubHTTPFacet` stays green there.
- `4ps` (irohup `-device`) is now a ~20-line wrapper over
  `nodeagent.EnrollDevice`; not wired into irohup yet.
- `~/git/nixos`: flake.lock bumped to `d6ae7d9` and switched (daemon
  `a33abr…`); the lock commit is the user's.
- ingress-nginx's `1gv` gate + `service.enabled: false` + the gateway's
  `HOST_IP` dial are dual-plane scaffolding: all three go when
  hostNetwork does (needs the TV off nebula → P2.4).
- Broken windows from the worker (report in git history of this
  handoff): jellyfin pod violates PSS `restricted` on server dry-run;
  ingress-nginx header comment still says "transport stays nebula".
- `0q0` blocked on capacity; `7c3`, `fgr`, `phz`, `t7b2` unchanged.

## Suggested next steps

- **P2.4** (`359.9.4`): Android/TV app swaps the nebula AAR for
  iroh + fake-IP; the zone rule (`nodeagent.Zone`) is the presentation
  contract it must share; `jellyfin.gw:8096` is the raw facet.
- Rebuild p0agent from main so nodes advertise their facets; then
  drop the `KindOf` fallback comment's "≤ 0.1.3" clause.
