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

- **Hub image on fly is behind**: `53q`, `2c2x`, `7w5` and the earlier
  `facethttp` pump all wait for `fly/deploy.sh`.
- `pymm` names a test that no longer exists (`TestMeshHTTPOverOverlay`)
  — close as already-fixed?
- Decisions parked from the swarm prep: `4xw7` (does android-p0
  survive), `5gz` (relay hook fail-open vs e8d), `fh2y` (drop
  wrong-target/unstamped too?).
- Follow-ups surfaced by workers (not filed): a shared liveness helper
  for `Resolve`/`NamesJSON` (`issuer.NameEntry.Live`); `NamesJSON`'s
  `Online` mis-reports a re-keyed name (first entry expired); `rejectStatus`
  still maps `ErrSig → bad-sig` from the loop (really a chain failure);
  `clusterwatch.step` dial failure leaves the storage row un-errored;
  gateway's `enrollHeadless` retries expired flows with no backoff;
  `image.nix` writes `.agent-report`/`.agent-log` on the disk workspace.
- **Adding a workload needs a line in `k8s/clusters/homelab/cluster.yaml`**
  as well as the directory (ApplicationSet list generator).
- Chaptarr/ABS first-run UI setup, Prowlarr indexers, Actual server
  password — unchanged from the previous handoff.
- `thz2`, `m6xc`, `d0o0.*` / `e6pu.2.*` awaiting close; the Mac's
  nix.conf (`nix-cache/bootstrap.md` §3).

## Suggested next steps

- Confirm-close the seven swarm beads (`bd show` each; reports in
  `/tmp/swarm/`), rebuild+pin the sandbox image, `fly/deploy.sh`.
- Decide the three parked questions above so a next swarm can take
  `4xw7`/`5gz`/`fh2y`.
- Back to `ivof` (sandbox-agent live acceptance, the parent actor).
