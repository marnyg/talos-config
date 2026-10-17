# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session
Swarm of seven over the ready queue (one wave, file-disjoint; reports
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
