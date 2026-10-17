# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session
**Wave 2** (five workers, all merged, beads noted, not closed):
`2004` rejectStatus drops the dead `ErrSig` case; `63u5` every pre-read
failure in `clusterwatch.step` marks the storage row too; `2nqw` gateway
`enrollHeadless` backs off 30 s→10 min between expired device flows
(`cmd/gateway/backoff.go`, + a `main_stub.go` so the package vets
untagged); `6tgj` `issuer.NameEntry.Live` shared by `Resolve` and
`NamesJSON` (`Online = len(live)>0`, `Node` = first live entry);
**`5gz` relay membership gate**: the iroh-relay child uses `[access.http]`
→ `POST /relay/access` on the hub (loopback-only, per-process bearer via
`IROH_RELAY_HTTP_BEARER_TOKEN`), admitting the hub's own id + witnessed
non-blocklisted members, **failing open while cold** (<5 min since start,
issuer not serving incl. the nag window, or no witnessed members);
`Mint` now witnesses so a fresh member's first beat crosses the gate;
mode + counters on the `/status` relay line (`relayadmit.go`).
Real-binary `TestRelayChild` passes with the hook (`IROH_RELAY_BIN`).
**Deployed 2026-10-17 (second attempt: the nix sandbox's missing CA bundle
made the hooked relay child panic in the image's test phase — see notes.md)**;
hub unsealed. Also landed: `runRelayChild()` out of `main`, `relayDeny`,
`envelope.VerifyAuthenticated` (the loop no longer repeats `serve`'s verify).
First live check: a member reconnect
after 5 min, watch the relay line's admitted/denied and the log for
`relay access: bad X-Iroh-NodeId` (header value assumed = iroh-base
`PublicKey` Display = lowercase hex; confirmed in iroh-base v1.1.0 source).

**Wave 1**: swarm of seven over the ready queue (one wave, file-disjoint; reports
under `/tmp/swarm/<name>.md`, beads noted, **none closed**):

- `3leu` — sandbox-agent's Forgejo header file moved onto a
  `medium: Memory` emptyDir at `/run/agent` (invariant 8). **Image not
  rebuilt**: `HUB_BUILDER=mar@nixos k8s/apps/sandbox/build.sh`, pin the
  digest in `agent.yaml`.
- `2c2x` — the hub's gitops row also lists the `platform-<name>`
  children: `— N children`, worst Running op, stalest / never
  reconciled, first off-state child; list failure keeps the root read.
- `53q` — `--public-url` (defaults to `--iroh-relay`);
  `hubManager.relayURL` beside `publicURL`; node configs carry
  `Hub`/`Relay` separately; `fly/entrypoint.sh` honours `PUBLIC_URL`.
- `4ps` — `irohup -device`: device-flow enrollment for headless
  hosts (one flow per run; denied/expired/sealed are fatal).
- `7w5` — protocol: an envelope whose signature fails now closes the
  stream unanswered (no signing oracle); `bad-sig` is no longer on
  the wire from `serve`.
- `fr9d` — `mobile.NamesJSON` is one pass over the name map.
- `d4p8` — the `wg0` flake assertion is a YAML-line regexp.

Gates: `go test ./...` in protocol + config-server, the tagged
`scripts/test-iroh.sh config-server` (19 pkgs green), oscheck for
`mobile` and `cmd/irohup`. Push runs the vendorHash dance (protocol/
changed).

Before that (2026-10-22 in the previous handoff's chronology): ArgoCD
became an app of apps (`pe8m`, ADR-0040 Accepted); CI feeds the Nix
cache (`0m50`, ADR-0039); Actual, Audiobookshelf + Chaptarr, the
admins gate as `global-auth-url`.

## Loose threads

- Same session, after the swarm: the seven beads + `pymm` closed;
  sandbox image `1146941` pushed and pinned; **hub deployed and
  unsealed** (53q/2c2x/7w5 live); `4xw7` done (iroh-go/{mobile,
  android-p0} deleted); `5gz` ruled fail-open-while-cold (note on the
  bead, still P3); `fh2y` closed by decision `24uf`. Debt filed:
  `2004` (rejectStatus ErrSig→unauthorized), `63u5` (storage row on
  dial failure), `2nqw` (enrollHeadless backoff).
- Not filed: a shared liveness helper for `Resolve`/`NamesJSON`;
  `NamesJSON.Online` mis-reports a re-keyed name (first entry expired);
  `image.nix` writes `.agent-report`/`.agent-log` on the disk workspace.
- **Adding a workload needs a line in `k8s/clusters/homelab/cluster.yaml`**
  as well as the directory (ApplicationSet list generator).
- Chaptarr/ABS first-run UI setup, Prowlarr indexers, Actual server
  password — unchanged from the previous handoff.
- `thz2`, `m6xc`, `d0o0.*` / `e6pu.2.*` awaiting close; the Mac's
  nix.conf (`nix-cache/bootstrap.md` §3).

## Suggested next steps

- Watch the new gitops row on `/status` (children) over a day; a
  sandbox-agent run to confirm `/run/agent` under kata.
- Back to `ivof` (sandbox-agent live acceptance, the parent actor).
