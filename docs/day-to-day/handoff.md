# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-10-18, the parent actor)

**ADR-0037's parent is built** (`ivof.4`–`ivof.8`; decision `4ijo`:
deploy before `bsj`, ns `sap`, hand-rolled OIDC RP, one child per task):

- `protocol/spawn`: `Spawner.Adopt`/`Children` — the born table as a
  cache refilled from a store after a restart. The test pins the two
  other restart duties the owner has: re-install issued consents (the
  envelope roots a child's chain in `Consents` before any handler) and
  seed `SeqBase` (replay refusal otherwise).
- `actors/parent` (C-free, `go test` untagged): `store.go` (modernc
  sqlite + FTS5; sessions/events/shares + the `children`/`consents`
  caches; session JSONL as files), `parent.go` (Submit = `#spawn` →
  birth → `#ping` → `#task`; the agent-events sink with resume; Steer/
  Abort stamped with the wallet; `#renew` gate; Beat: sweep, snapshot,
  lose lapsed, retire + `#kill`; Restore), `oidc.go` (PKCE + RS256/JWKS
  RP, HMAC cookie), `web.go` + `ui/` (list, session page, SSE tail,
  search, shares; ACL in every route).
- `actors/child`: **the upload is resumable** — found by the restart
  test: the old one-shot POST stranded a run's tail, PR pointer and
  session file whenever the parent went away. `GET /events/<id>` →
  `{count}`, `POST ?from=N`, PUT retried.
- `cmd/parent`, `actors/image.nix` (+`parent`, `/var/lib/sap-parent`),
  `k8s/apps/sap-parent` (PVC, Deployment, Service, Ingress
  `agents.gw.mesh.internal` opted out of the admins gate like ArgoCD),
  siwe-oidc `-client=agents`, app-of-apps line. New `vendorHash`.

## Loose threads

- Bootstrap done: parent id `ed:9608…c9c3` is the lighthouse's
  `-member` and the provisioner's `-customer`; all three `sap-*` run
  `sap-actors:7751fa0`; `sandbox-agent` is `sha256:ec97d7…`.
- Not confirmed live: sharing to a second wallet (none allow-listed at
  hand; the ACL is under test).
- A fresh `sandbox-agent` tag costs ~1–2 min of `pending` on first
  birth per node (image pull, `9g6m`); the 5-min `-window` covers it.
- Share list is wallets/`group:<name>`; the UI assumes the groups
  siwe-oidc puts in the token. Nothing verifies a shared wallet is a
  known one (it just never logs in if it is not).
- `h5ao` (child Dial failure after the ack is invisible) is half
  answered by the resumable uploader (it retries for the run's life);
  the "never reached at all" half still surfaces only as a session
  that stays running until lapse → `lost`.
- Carried: `d0o0` in progress; `b35t` (outfit digest pinned by hand —
  now in three places with `sap-parent -image`); `tmm5`; `xrfm`;
  ntfy phone subscription + first real page (`88uq`).

## Suggested next steps

- `bsj` (Longhorn backup target): the parent's store is the first
  thing worth backing up; `ivof` closes on it.
- Use it: a few real tasks from the phone; the terminal client
  (`hp79`) when the web UI's shape has settled.
- Protocol threads: `qx5c` (relay admission), `h5ao`'s remaining half.
