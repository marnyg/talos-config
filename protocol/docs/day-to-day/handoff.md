# Handoff — sovereign-actor protocol

<!-- "Where we left off" for the protocol scope. Overwritten per session.
     Deployment (talos/hub/fly) context lives in the root handoff. -->

## Last session

2026-09-29 — **M4.6 acceptance run passed on both drivers; ADR-0008
and ADR-0009 Accepted.** Nothing under `protocol/` code changed.

- **`actors/cmd/spawn`** (`676a791`): the laptop parent. Persisted
  key (`-state`, default `~/.sap-parent`), `-print-id` for the
  provisioner's `-customer`, loads the provisioner's `location.json`,
  `#spawn` by digest, one `#ping`, then counts `#renew → #extend`
  rounds and **goes deaf after `-renewals`** (default 1: refuses
  `#renew`), exiting when its born table drops the child. Defaults
  `-window 3m`, `-kit-ttl 5m` so the first beat extends.
- **docker** (native provisioner on the laptop against Docker
  Desktop): born 1.5 s, ping ok, 1 extend, child self-lapsed at the
  chain's exp (12:18:28), provisioner swept at 12:18:44.
- **k8s** (`k8s/apps/sap-provisioner`, nas1): born 0.7 s, ping ok,
  1 extend. The Job outlived its 180 s window deadline, so the
  `activeDeadlineSeconds` patch works live; it then ended
  `DeadlineExceeded` at the extended `until`, the same second the
  child's own lapse was due. Logs in `/tmp/sap-run/` (ephemeral).
- Sketch § Spawning rewritten to the built shape (provisioner actor,
  birth consent, nonce-as-correlation, passive leases, self-lapse);
  exploration-log §M4 pruned (all of it is in the ADRs' options).

## Loose threads

- **Child logs die with the Job** (`ttlSecondsAfterFinished`): on k8s
  the child's own "lapsed" line was lost, so which mechanism ended the
  pod (platform deadline or self-lapse) is not recorded. Both converge
  by design; a longer TTL or a log tail would settle it.
- Fixed after the run (`70c2531`): the provisioner logs each
  `#spawn`/`#extend`/`#kill` (`TestLeaseLog`); `actors/keyfile` is the
  one key loader; the docker driver follows the docker context.
- Discovery is still a file (`location.json`, 10 min); the in-cluster
  one has to be read with `kubectl debug` (the image has no shell).
- Carried: customer consent = a year-long flag; registry auth not v0;
  `payment` absent (M5); `fh2y`; open problems 8, 9; `bh74` shows on
  provisioners too.

## Suggested next steps

- Owner picks the next direction: M5 money (`0bc.5`), or the
  lighthouse `#publish` for provisioners (removes the file handoff).
