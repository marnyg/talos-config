# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-07-31 (fifth session) — **the storage layer landed** (`d43b8fb`
through `5069649`). ADR-0011 went from Proposed to Accepted and was
implemented the same day.

- **Invariant 2 was amended before any code moved** (decision
  `5f9040ab`). The old wording named `u-media` as the one instance of
  state outside git; the replacement draws a layer line instead — git
  owns the control plane, the data plane owns payload *and the
  bookkeeping a stateful service keeps about it* (Longhorn's CRDs).
  Net effect: stronger per node ("no single disk is exempt from a
  wipe"), honest about the data plane. `reinstall.md` was rewritten
  around it as one whole-disk procedure for every node.
- **Both nodes upgraded** to schematic `6a9acc…` (nebula + iscsi-tools
  + util-linux-tools), and **Longhorn 1.12.0 runs on 1073GB raw** —
  w1's 751GB plus cp1's 322GB, the former `u-media` partition handed
  over with no reinstall, exactly as the ADR promised.
- **The media volumes forced a design call the ADR had missed.** They
  are *shared* (tv by sonarr+jellyfin, downloads by four pods), which
  hostPath only allowed because every pod sat on cp1. Longhorn RWO
  attaches to one node, so a naive migration would have re-pinned all
  six. Resolved by splitting StorageClasses **by data class**: default
  `longhorn` (RWO, 2 replicas) for app state, `longhorn-bulk` (RWX, 1
  replica, Retain) for the library.
- **Mobility was verified deliberately**, not observed: a file written
  from a sonarr pod on cp1 was read back intact after cordoning cp1 and
  letting the pod reschedule to w1. Media pods now run split across
  both nodes sharing the same volumes.
- **SSO broke and was fixed properly.** oauth2-proxy CrashLooped for
  ~70min after cp1's reboot moved it to w1: its `hostAliases` pinned the
  issuer to cp1's *mesh* address, and nebula carries only 10.42.0.0/16
  sources, so a pod at 10.244.x.x cannot dial it. Now aliased to the
  siwe-oidc ClusterIP (pinned in git), which satisfies OIDC issuer-URL
  equality rather than dodging it — the discovery document advertises
  the mesh name however it is fetched.

## Loose threads

- **A knowing deviation from invariant 2 is live** (decision
  `d5f73e89`): `longhorn-bulk` is 1 replica, so the library is neither
  git-derivable nor replicated and a wipe of its node destroys it. The
  invariant stands as written — the implementation is the wrong half,
  accepted only until the new nodes add capacity (`da61bd8e`).
- **Nothing on this cluster is replicated yet.** The 2-replica class has
  no users: every app still keeps config on `emptyDir`, so
  sonarr/radarr/jellyfin metadata dies on every *pod restart*. This is
  the last unclaimed piece of ADR-0011 and the only one that makes a
  node wipe genuinely cheap.
- **`argocd-server` has the same mesh-alias bug oauth2-proxy just had**
  (`f9bac57c`) and is currently broken for SSO login. Its pin lives in
  the bootstrap-only installer Job, so it needs migrating into
  `k8s/apps/argocd/`, not a one-line edit.
- **etcd advertises the DHCP lease** (`6c456522`), violating invariant 7.
  Cleared by a reboot this session, not fixed; the mesh-advertise fix
  narrows listen addresses too and would kill kube-apiserver's loopback
  etcd dial unless `listenSubnets` explicitly retains it.
- No backup target (`8b9972fd`), which invariant 2 makes load-bearing.

## Suggested next steps

- Move app config off `emptyDir` onto the 2-replica class — verifying
  each app's `configurator` container is idempotent against pre-existing
  config rather than assuming.
- Fix `argocd-server`'s alias the same way oauth2-proxy was fixed.
- When the new nodes land: onboard them on `6a9acc…` directly, then
  raise `longhorn-bulk` to 2 replicas.
