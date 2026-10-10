# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-12: **`e6pu.2` built by swarm — the child is an agent.** Three
workers, three `--no-ff` merges (`8c0488b`, `fdde3cc`, `055327a`), all
gates re-run by the orchestrator.

- **`facethttp` → `protocol/facethttp`** (`e6pu.2.1`, decision `k5hb`):
  rename, four hub/gateway consumers repointed, `hubcaller.facetStreamConn`
  lifted into `facethttp.Dialed` (dial-side `net.Conn`), stdlib test.
  config-server vendorHash bumped — note the stale-FOD trap in notes.
- **`actors/pirpc`** (`e6pu.2.2`): Go driver for `pi --mode rpc` —
  strict-LF JSONL, id-correlated responses, `Events()` raw stream,
  `Close` = close stdin then SIGKILL after `KillAfter`. Fake-pi tests;
  `PIRPC_REAL=1` ran against pi 1.0.3.
- **child `#task`/`#steer`/`#abort` + uploader** (`e6pu.2.3`,
  `actors/child/task.go`): `#task` acks `{started, session}` at once,
  runs pi, streams every record as one chunked `POST /events/<id>` to
  the parent's `agent-events` stream facet, `PUT /session/<id>` at
  settle, last line `child_settled`. **Chain-on-connect:** preamble
  `{chain, speak_as}`, parent admits with `cert.VerifyChain` (not
  `Authorize`). `cmd/spawn -prompt` is the parent for the acceptance
  run (prints events; seed of the CLI `hp79`); `cmd/child` reads
  `SAP_PI`, `SAP_PI_ARGS`, `SAP_WORKDIR`.

## Loose threads

- `e6pu.2.{1,2,3}` merged, **awaiting owner close**; `e6pu.2` then
  closes too. Not live yet: the sandbox-agent image needs
  `/usr/local/bin/child` + an entrypoint that runs the beat beside pi
  (`k8s/apps/sandbox/image.nix`), then one spawn with
  `-runtime-class kata -outfit … -prompt` — the ADR-0037 confirmation
  step.
- The git half (`{branch, head, pr}` pointer) was ruled out of
  `e6pu.2`; filed as `e6pu.3`. ADR-0037 "→ pointer" is now "ack +
  pointer on the stream" (see the ADR question in the session report).
- Threads from the reports: a `Dial` failure after the `started` ack is
  invisible to the parent; `Spec.Outfit` doc should say Outfit certs
  are not auto-installed as consents (`a.EditConsents` is the caller's
  job); `streamfacet.Conn.Open` doc should say "any number of times".
- Standing: 9 `<!-- stale? -->` flags; `jlgz`, `bsj` (blocks `ivof`),
  `bhui`.

## Suggested next steps

- Rebuild `sandbox-agent` with the child binary and run the live
  acceptance (`spawn -prompt`), then close `e6pu`.
- The parent actor proper (store schema, `#task` relay, UI behind the
  person gate) — no bead yet; `hp79` (CLI) follows it.
- `e6pu.3`: the git workspace/PR half inside the child.
