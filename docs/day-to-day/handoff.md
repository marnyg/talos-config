# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-29/30 — **HA sweep slice 1 (`9l67`) done, plus a runway bug
it uncovered (`5hek`).**

- **GitOps unfrozen**: out-of-service taint on (powered-off) w1
  released the ghost pods + VolumeAttachments in 30 s; ArgoCD op
  completed. Structural: Longhorn `nodeDownPodDeletionPolicy:
  delete-both-…`, argocd controller pinned to the control plane
  (`k8s/apps/argocd/controller-patch.yaml`), and a `/status` **gitops**
  row — the hub reads `argocd/apps` over cp1's `kube-api` facet
  (`config-server/gitops.go`, policy row `{facet: kube-api, host: hub}`)
  and warns on reconcile > 20 min / op Running > 30 min. `c33c305`.
- **Member runway was 3.5 d, not 30 d** (`5hek`, `6ccabed`): the beat
  grant that authorises `#renew` was 7 d, so the gateway (8 d on dead
  w1) was stranded with a live member cert. `BeatGrantTTL = MemberTTL`,
  `actor.RenewLifetime` migrates old 7 d grants on first renewal, the
  agent re-enrols instead of retrying an expired kit; `runway.qnt`
  finding 8, domain model updated. Hub deployed + unsealed; cp1
  renewed; gateway re-enrolled (image `ff7c478`, same NodeId), kit
  verified 90 d/90 d. `*.gw.mesh.internal` back.

## Loose threads

- **w1 still carries the out-of-service taint** — remove it before it
  rejoins; its kit is expired, so it needs its config re-served (one
  wallet act). Recipe in notes 2026-09-29.
- The gitops row's first live poll was admitted at cp1 (09:12:50Z); I
  did not see the row rendered — owner confirms on `/status`.
- `5q33`: gateway done; phone/TV APK and Mac daemon still pre-P4.2.
- Test flake filed: `d4p8` (`wg0` substring in base64).

## Suggested next steps

- HA sweep slice 2: ingress-nginx / siwe-oidc / oauth2-proxy replicas
  + anti-affinity (three nodes now).
- Slice 3 (gateway ephemeral key) needs a decision against invariant 2
  (`359.9.3`) before any code — and `5hek` makes the stateful gateway
  cheaper to keep (a 90 d kit survives a long outage).
- Populate nas1's SATA bays.
