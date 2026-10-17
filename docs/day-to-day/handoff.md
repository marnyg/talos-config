# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session
2026-10-17: **Home Assistant deployed, and the first WebSocket ever
to complete through the gateway.** `k8s/apps/homeassistant/`
(`17e576d`, `6ac688a`): Longhorn 10Gi, PSS baseline (s6 needs root),
`home.gw.mesh.internal` behind the admins gate, image `2026.10.2` by
digest. Onboarding stalled on `wss://…/api/websocket` and the dig
found a protocol bug (`o8wd`, closed): `facethttp.Conn` ignored read
deadlines, so `http.Server.Hijack` (what `ReverseProxy` does for a
101) blocked in `abortPendingRead` until the *peer* closed — every
WebSocket through the gateway (HA, Grafana live, Vaultwarden
`/notifications/hub`) died at the browser's 20 s open timeout. Fix
`109c6b5`: a read pump in `protocol/facethttp` serves `Read` from a
bounded buffer under a deadline, and `Close` FINs the send side
before closing (the iroh-tagged `TestNodeAgentEndToEnd` caught the
reset-discards-response regression). Gateway image `109c6b5` is live
(`d5c8a27`); verified `101` + `auth_required` end to end with curl.
Second finding: HA 2026.x migrates an `http:` YAML block ONCE into
`.storage/http` as a pending trial needing UI promotion within 5 min;
the first start lost that race (no WebSocket) and HA reverted to no
trusted proxies. The init container now runs the HA image and
reconciles `use_x_forwarded_for`/`trusted_proxies` into the store's
`stable` before every start (`6ac688a`).

Previous session (2026-10-16): Vaultwarden (`l5v8`, `eb1ad52`).

## Loose threads

- HA onboarding: owner `mar` exists; the remaining steps (location,
  analytics, integrations) and the companion app on the Sony are
  untested. `b311` holds the discovery/USB question.
- The hub image on fly still carries the pre-pump `facethttp`
  (hub-http). Nothing there hijacks, so no urgency; the next
  `fly/deploy.sh` picks it up. Same for irohup/nodeagent builds.
- Vaultwarden live sync and Grafana live should now work; neither
  re-tested after the gateway roll.
- `facethttp.Conn` now honours `ReadHeaderTimeout` (10 s after the
  first byte) for real; a pump goroutine still outlives a stream whose
  peer vanishes (FFI read not interruptible, as before).
- Vaultwarden dates in `k8s/apps/vaultwarden/deployment.yaml` say
  2026-10-19; the commit is 2026-10-16.
- From before: `thz2`, `m6xc`, `d0o0.*` / `e6pu.2.*` awaiting close,
  9+ `<!-- stale? -->` notes.

## Suggested next steps

- Finish HA onboarding in the browser; pair the companion app.
- Re-check Vaultwarden extension sync / Grafana live over the mesh.
- Back to `ivof` (sandbox-agent live acceptance, the parent actor).
