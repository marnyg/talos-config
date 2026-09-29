# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

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

- `apps` shows `OutOfSync` / `Progressing` after the catch-up sync
  (the `cdis.cdi.kubevirt.io` CRD is the one OutOfSync resource). Not
  investigated; may be long-standing (compare `p0ar`).
- The other argocd Deployment replicas on w1 are ghost `Terminating`
  pods too. They're harmless (replacements run on cp1), but they're the
  same class of problem.
- nas1 is Ready and carries the provisioner. w1 is still off (`9l67`).
- Carried: nas1's SATA bays empty; `5q33`, `4te`, `t7b2`, `c4vd`,
  `etzl`.

## Suggested next steps

- The HA sweep (`9l67`) has a second, sharper reason now: one node
  down silently stops GitOps.
- Populate nas1's SATA bays.
- Protocol: owner picks M5 (`0bc.5`) or lighthouse discovery for
  provisioners.
