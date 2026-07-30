# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-07-31 (second session) — **service exposure moved to nebula-native
ingress** (`b522a14`..`ad9ea78`, ADR-0009; task 37). Deployed, unsealed,
and verified end-to-end.

- Hub DNS gained one rule: `<service>.<member>.mesh.internal` resolves
  to the member (`dnsRespond`, +16 lines). Exposing a service is now
  purely an Ingress in git — no meta.yaml edit, no deploy, no unseal.
  A flat-alias design was built first and discarded same-session
  (deploy+unseal per service; see ADR-0009 Considered Options).
- tailscale operator/ingressClass deleted; ingress-nginx (DaemonSet,
  hostNetwork :80, privileged-PSS namespace via
  `managedNamespaceMetadata`) replaces it. All six media services
  verified by hostname over the mesh from the laptop.
- ADR-0008 (media user volume) reviewed → **Accepted**.
- SSO design sketched and crystallized: stateless SIWE→OIDC bridge
  (reuse `ethsig`/`deviceflow` patterns, PKCE-only, ephemeral JWKS,
  clients from git), oauth2-proxy via nginx `auth_request` for
  non-OIDC apps. Sketch 162c630d closed into tasks 38–42.
- `nebup -reenroll` run by the user; the stale pre-phase-2
  `laptop.yml` thread is closed.

## Loose threads

- **Task 37 open pending explicit confirmation** — rollout verified
  (all six services answer by name; tailscale ns pruned cleanly), user
  hasn't said "done" yet.
- ~~Broken windows~~ all fixed at session close: deployment.md access
  section updated, sonarr/radarr NodePorts dropped (jellyfin's 30096
  kept for LAN-direct TV; transmission-peer is peer traffic, not web),
  notes.md dex line struck through. Goal for the SSO arc added to
  `goals.md` same session.
- Media library still empty (u-media re-adoption unexercised with real
  data).

## Suggested next steps

- Task 38: the SIWE→OIDC bridge service — the rest of the SSO stack
  (39–41) hangs off it. Redirect URIs can now be real
  (`http://argocd.cp1.mesh.internal/...`).
- Task 39 is the cheapest standalone win (ArgoCD OIDC + dex deletion
  kills the long-standing Error pod).
- Refill the media library.
