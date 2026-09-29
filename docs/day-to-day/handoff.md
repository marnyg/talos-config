# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-29 (second session) — **Lighthouse discovery for provisioners
(`0bc.6`) built and accepted on docker; k8s manifests written, not
applied.** Detail in `protocol/docs/day-to-day/handoff.md`.

- `protocol/actor` gains `Bootstrap` hints (the network bundle's raw
  endpoints, invariant 11); `actors/cmd/lighthouse` is new; the
  provisioner `#publish`es and `spawn` `#lookup`s. `actors-bin`
  vendorHash bumped (first import of `protocol/lighthouse`).
- `k8s/apps/sap-lighthouse` + a commented `-lighthouse=` on the
  provisioner Deployment. **The pinned image (`53b84b4`) has no
  `lighthouse` binary** — ArgoCD will sync the new Deployment into
  CrashLoop until `actors/build.sh` pushes a new digest and both
  manifests are re-pinned. Then: read the lighthouse id from its log,
  fill in the provisioner flag, run `spawn -lighthouse …
  -provisioner-id ed:9c3ae5ec…` from the laptop.

## Session before

2026-09-29 — **Protocol M4 accepted live; the cluster's first actor
workload deployed; ArgoCD un-stuck.**

- `actors/cmd/spawn` (laptop parent) + `k8s/apps/sap-provisioner`
  (ns `sap`, Jobs-only Role, PVC; `676a791`). The M4.6 acceptance run
  passed on the in-cluster k8s provisioner (nas1) and a docker one on
  the laptop. ADR-0008/0009 Accepted. Detail in
  `protocol/docs/day-to-day/handoff.md`; deployed facts in
  `technical/deployed-state.md`.
- `ghcr.io/marnyg/sap-actors` is **public** now (owner, GitHub UI).
- **ArgoCD had reconciled nothing since 2026-09-21**: its controller
  StatefulSet pod was stuck `Terminating` on dead w1. Force-deleted, so
  it now runs on cp1 and synced `676a791`. Nothing was lost: the only
  `k8s/` change in the gap was a comment. Noted on `9l67` and in
  `notes.md`.

## Loose threads

- Broken windows fixed (`70c2531`). The CDI CRD no longer declares
  `v1alpha1`: ArgoCD selfHeal and cdi-operator had been rewriting it
  against each other (generation 7210), and `apps` is **Synced** now.
  33 ghost ReplicaSet pods on w1 were force-deleted.
- **Held on purpose:** the ghost `gateway` pod on w1 (Recreate: deleting
  it would restart the gateway, which undoes the "not by hand" call,
  `9l67`), Longhorn's instance-manager and the `win2k25` virt-launcher
  (their operators own them). `apps` health reads Progressing only
  because of `gateway` 0/1.
- nas1 is Ready and carries the provisioner. w1 is still off (`9l67`).
- Carried: nas1's SATA bays empty; `5q33`, `4te`, `t7b2`, `c4vd`,
  `etzl`.

## Suggested next steps

- The HA sweep (`9l67`) has a second, sharper reason now: one node
  down silently stops GitOps.
- Populate nas1's SATA bays.
- Protocol: owner picks M5 (`0bc.5`) or lighthouse discovery for
  provisioners.
