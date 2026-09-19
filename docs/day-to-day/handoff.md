# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-19 — **cp1 is the first real member of the identity plane**
(`359.8.3` closed). At 13:41:40Z, uptime 10.7 s, extension 0.1.1:
`key loaded` (the P0.3 NodeId `ed:7dd90eb3…` kept) → `enrolled: member
"cp1" groups [machines]` → `beat ok … 1 names`. Hub deployed at
`1d5baa8`, unsealed twice. Commits `7c740ff` `1284e40` `f32474a`
`6050d87` `1d5baa8` `8f8c797` `5b53ea0`.

- **Stream facets on the wire** (`iroh-transport/streamfacet.go`):
  `Options.StreamALPNs`, `AcceptConn`/`DialConn`; the caller's
  `cert.Bundle` rides the first bi-stream (`cert.EncodeBundle`), the
  acceptor answers `ok` / `refused: <reason>`, every later bi-stream is
  a `Raw` forward. The connection is the invocation, checked once.
- **ADR-0015 boot enrollment built** (`config-server/boottoken`,
  `nodeagent`, `nodeenroll.go`): `/config` injects a `p0agent`
  ExtensionServiceConfig `{hub, relay, token}` when `--iroh-relay` is
  set; `POST /mesh/enroll/node {node, token}` → Kit, name from
  `meta.yaml`, group `machines`. Token: HMAC of
  `masterderive.BootTokenKey`, 1 h TTL, nonce, volatile `Seen`
  (released if the mint fails; a sealed Issuer never burns it).
  Verification lives in the HTTP handler with the master (decision
  `488`), not a Provisioner facet.
- **`config-server/nodeagent` + `cmd/nodeagent`**: enroll-or-load Kit,
  beat (`#renew` past half-life or on hub rotation, `#bundle` 6-hourly),
  persists `kit.json` `bundle.json` `hub.json` `mark` beside `key`,
  self-signed consent to the wallet for exactly the forwarded facets,
  `cert.Authorize` on accept, splice to `apid`/`kube-api`. E2E test
  `TestNodeAgentEndToEnd` (enroll → beat → name map → admin admitted,
  media + stranger refused → restart from state).
- **Two protocol findings** (`protocol/actor`): a restarted sender's
  `seq` restarted at 1 against the hub's surviving high-water mark →
  `Actor.SeqBase` (agent seeds `UnixNano`); stream-facet verifiers sit
  outside the inbox → `Actor.Observe` / `RestoreLowWater`.
- Ops learned: Talos does **not** restart an extension service on an
  ExtensionServiceConfig change (`talosctl service ext-p0agent
  restart`); a scratch rootfs needs `/etc/ssl/certs` bound for Go HTTPS
  (0.1.0 → 0.1.1); the device flow works with one browser click
  (`notes.md`).

## Loose threads

- **ADR-0015 promoted to Accepted** this session; **ADR-0024** still
  Proposed (Provisioner-as-actor). `nebderive.MachineKey` is now dead
  on the identity plane but still feeds the nebula patch until Phase 4
  (`359.11.2`).
- No real caller has hit cp1's `apid` facet yet — `359.8.4` (irohup)
  is that; `nodeagent_iroh_test.go`'s `device()` is its script.
- A node re-enrolling after its Kit expired (off > 90 d) needs a fresh
  config serve — the token in the stored config is long dead. Not
  beaded; surfaces only with a long outage.
- `hub.publicURL = --iroh-relay` assumes one hostname for HTTPS and the
  relay (true on fly). `p0agent`'s `serve` half is superseded by
  `cmd/nodeagent`; delete with `359.8.4`. `Dockerfile.siweoidc` still
  broken. Protocol: `t29` now has its third consumer-driven runtime
  addition (`Hold`, `Multi`, `SeqBase`/`Observe`).
- Carried: `tqr` (flip `/sealed` on identity — a member has beaten now),
  `kql` (scratch relay teardown — unblocked), w1 down (`0q0`, `kso`),
  `5gz`, `DefaultMailbox`/beat fraction unbeaded.

## Suggested next steps

- **`359.8.4`** irohup: enrollment → Kit, beat, `DialConn(nodeID,
  hints, policy.ALPN("apid"), EncodeBundle(bundle))` → TCP bridge;
  `talosctl` through it is the first real stream-facet call. Name map
  from `#bundle` gives `cp1`'s NodeId + reach-me-at.
- **`kql`** tear down the scratch relay; **`tqr`** flip `/sealed`.
- **`359.8.6`** exit checks: reboot cp1 and watch `ext-p0agent` beat
  unaided (the restart path is tested in-process, not yet on the box).
