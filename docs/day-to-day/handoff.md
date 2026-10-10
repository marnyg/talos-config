# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-11: **designed the agent backend** (grill-design session, no
code). ADR-0037 (Proposed): the **parent actor** is the backend —
spawns agents as children, receives each child's pi event stream over
a stream facet, keeps sessions in its own SQLite+FTS5 on a Longhorn
PVC, serves list/watch/steer/search behind the person gate. Results
are **Forgejo branches + PRs**; the `#task` reply is a pointer.

- Credentials are provisioner policy keyed on image (`{openrouter,
  forgejo-agent}` for `sandbox-agent`); `#spawn` and `#task` carry
  names, never secrets. Spike `ivof.3` closed on that ruling.
- Ingest: the child beat runs `pi --mode rpc` — prompt down, events
  up, `steer`/`abort` down, close stdin = shutdown; session JSONL
  shipped at settle for resume.
- Forgejo (`ivof.1`): ns `forgejo`, sqlite3 + PVC, `git.gw.mesh.internal`,
  `siwe-oidc -client` (not `-token-client`), no `auth-url` (git clients
  have no cookie); orgs `agents` + `mirrors`; `talos-config` stays on
  GitHub (decision `4la8`), signed merges on the laptop.
- Glossary: **Session**, **Workspace** (root); "durable state is a
  data-plane resource" (protocol). Exploration-log has the rule-outs.
- Deferred: `264f` volumes-on-the-lease (ADR-0005: wait for a spawned
  consumer). Epic `ivof` now depends on `bsj` (backup target).

## Loose threads

- ADR-0037 is Proposed — owner flips to Accepted. ADR-0036 too.
- Invariant 2 consequence stated in ADR-0037: the parent is a
  **durable-key** actor; its volume inherits `9io`.
- Forgejo tokens are per *user*: "org-scoped token" means a bot user
  in `agents`. The docs say so; `ivof.2`'s title still says
  "SealedSecret in ai" (true for the interim).
- Egress unbounded, budget = OpenRouter spend limit (ADR-0036 gaps;
  `0bc.5`). `kata-qemu` handler on nodes, not in git.
- `bd` CLI timed out once at 20 s on a plain `note` (see notes.md).
- Standing: herdr 0.9.3 now; `jlgz`, `bsj`, `bhui`; 9 `<!-- stale? -->`
  flags in `notes.md` await the owner.

## Suggested next steps

- Build order: `ivof.1` Forgejo → `ivof.2` `agent-run.sh -r/-b` (interim,
  no protocol) → `e6pu` (driver RuntimeClass + secret sets, child beat
  with pi RPC client, `#task`) → the parent (store, UI). A swarm was
  about to be prepared for the first two.
- `bsj` before the parent's store holds anything you'd miss.
