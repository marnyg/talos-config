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

- **Two-step bootstrap not done:** the pod will log `parent: id id=ed:…`
  and warn `#lookup failed`; that id must be added to
  `k8s/apps/sap-lighthouse` (`-member`) and `k8s/apps/sap-provisioner`
  (`-customer`), and the image digest in `sap-parent/deployment.yaml`
  is still the provisioner's old `sap-actors` pin — `actors/build.sh`
  must push a new image (it now carries `parent`) and all three
  `sap-*` deployments should move to it.
- ADR-0037's confirmation is live-only (see focus.md). Things to watch
  on the first run: the SSE tail through ingress-nginx (buffering off,
  read timeout 3600 set), the OIDC round trip with the `hostAliases`
  pin, kata birth time vs `-window` (5 min).
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

- Push; `actors/build.sh`; pin the new digest in the three `sap-*`
  deployments; wait for the `sap-parent` pod; copy its id into the
  lighthouse/provisioner args; push again. Then the phone run.
- Close `ivof.4`–`ivof.8` once confirmed live; `ivof` itself stays
  open on `bsj`.
- Then back to the open protocol threads (`qx5c`, `h5ao`) or `bsj`.
