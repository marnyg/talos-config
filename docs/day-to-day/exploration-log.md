# Exploration Log

<!-- "What we've tried and ruled out." Prevents re-attempting dead ends across sessions.
     Granularity: strategy-level pivots only. Not "used ripgrep instead of sed".
     Yes: "tried library X, ruled out for reason Y." -->

## Pod resolution of mesh names (2026-07-31)

- 2026-07-31 — Tried to give cluster pods `.mesh.internal` resolution
  via Talos `extraHostEntries` + `hostDNS.forwardKubeDNSToHost` (one
  machine-config line, cluster-wide). Ruled out **before deploying**:
  Talos host DNS explicitly does not serve `/etc/hosts` entries to
  kube-dns queries (siderolabs/talos#9822, #13141) — it would also
  have cost a fly deploy + unseal + apply cycle. CoreDNS Corefile
  surgery ruled out too: Talos re-applies its bootstrap manifests, so
  ConfigMap edits revert on reboot and nothing in machine config
  exposes the Corefile. Landed on: `hostAliases` pinning
  `auth.cp1.mesh.internal` → cp1's derived mesh address on each
  relying-party pod (argocd-server via installer-job patch;
  oauth2-proxy and jellyfin in their manifests). Only the issuer name
  needs this — browsers resolve via hub DNS; pods never dial other
  service names.
- 2026-07-31 (later) — **that landing was half wrong, and the wrong
  half was the address, not the mechanism.** `hostAliases` is still
  right; pointing it at cp1's *mesh* address is not. nebula carries
  10.42.0.0/16 source addresses and a pod's source is 10.244.x.x, so
  the alias only resolves-and-connects while the pod happens to run on
  cp1 — anywhere else the dial times out. cp1's reboot moved
  oauth2-proxy to w1 and every SSO-protected service returned 500 for
  ~70 minutes. Landed on: alias the issuer name to the **siwe-oidc
  Service ClusterIP** (pinned in git). This satisfies OIDC's issuer-URL
  equality rather than dodging it — siwe-oidc advertises issuer,
  authorization_endpoint and jwks_uri as `http://auth.cp1.mesh.internal`
  no matter how the discovery document is fetched, so only the transport
  differs: server-side calls resolve in-cluster from any node,
  browser-side calls still traverse the mesh via ingress.
  The sentence above — "pods never dial other service names" — was the
  buried assumption that made this a latent single-node bug.
  ~~`argocd-server` still carries the old pin (`f9bac57c`)~~ fixed
  2026-08-05 (`fed04b4`, SSA partial manifest), plus a third instance
  in jellyfin (`f1f5dd4`).

## TV mesh client (2026-07-30)

- 2026-07-30 — Considered the official Mobile Nebula app as the Android
  TV client. Ruled out: Flutter buttons ignore d-pad clicks on Google
  TV (DefinedNet/mobile_nebula#148), no camera for the QR import path,
  and Play won't list it on TV (no leanback intent). A BT-mouse
  sideload workaround exists but is not an acceptable steady-state UX.
  Landed on: home TV stays off the mesh (LAN-direct Jellyfin, decision
  3dfef644); a thin Kotlin/leanback APK bundling the nebula gomobile
  AAR is the design if a remote-TV need ever appears (task 2e1bef85).
  Phone path unaffected — verified in source that the app imports
  externally-derived private keys (`add_certificate_screen.dart`).
