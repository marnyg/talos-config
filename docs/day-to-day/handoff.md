# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-11: **designed the agent backend (ADR-0037) and built its first
two slices by swarm** — three workers, three merges, all live.

- **Forgejo is on the cluster** (`ivof.1`, `fe4ca92`): ns `forgejo`,
  sqlite3 on a Longhorn PVC, `git.gw.mesh.internal`, no Ingress gate,
  person-gated OIDC client of `siwe-oidc` (PKCE verified). Bootstrapped
  by hand per `k8s/apps/forgejo/bootstrap.md`: `sysadmin` (break-glass),
  auth source `siwe`, orgs `agents`/`mirrors`, bot `agent` in team
  `bots`, token sealed as `forgejo-agent` in `ai` (`97d1063`).
- **k8s driver outfit + RuntimeClass** (`e6pu.1`, `60abdc1`):
  `Config.{RuntimeClass,Outfit}`, provisioner `-runtime-class`,
  `-outfit IMAGE=SECRET,…`; `nix build .#actors-bin` green.
- **`agent-run.sh -r owner/name [-b ref]`** (`ivof.2`, `8d6943a`, image
  `sandbox-agent:8d6943a`): clone → pi → push `agent/<job>` → PR with
  the report as body, SIGTERM-safe. **Live: PR #1 on `agents/scratch`**
  from Job `agent-scratch-1` (kata guest kernel 6.18.5).
- Docs: ADR-0037 (Proposed), glossary Session/Workspace/Forgejo (root),
  Outfit + "durable state is a data-plane resource" (protocol),
  exploration-log rule-outs. Decision `k5hb`: `facethttp` → `protocol/`.

## Loose threads

- `ivof.1`, `ivof.2`, `e6pu.1` closed; ADR-0037 Accepted. ADR-0036 still
  Proposed. The `sysadmin` password is sealed as
  `k8s/apps/forgejo/sysadmin.sealed.yaml` (the cluster is the vault).
- `a-agent-run` worker died on the **Anthropic monthly spend limit**
  after committing; its report is missing — the orchestrator reviewed
  the diff instead. Check the account before the next swarm.
- Threads filed: `8qwk` (outfit keyed on exact digest ⇒ re-pin needs a
  provisioner redeploy), `dimt` (`REQUIRE_SIGNIN_VIEW`, `--admin-group`).
- Broken windows from the reports were fixed the same session (siwe-oidc
  Service comment, provisioner `err` shadow, fake API namespace).
- Forgejo's Ingress now carries the group gate (`dimt`; the "git clients
can't pass it" reasoning was wrong — ADR-0037 amended). **One UI click
left for you:** org `mirrors` → settings → Visibility → Public, so mesh
members read mirrors without a Forgejo login (the API PATCH is a no-op).
- Standing: 9 `<!-- stale? -->` flags; `jlgz`, `bsj` (now blocks `ivof`),
  `bhui`.

## Suggested next steps

- `e6pu.2`: child beat driving `pi --mode rpc` over a stream facet —
  starts with moving `facethttp` to `protocol/` (`k5hb`).
- Then the parent actor (no bead yet: store schema, `#task`, UI).
- Add `mirrors/talos-config` as a pull-mirror and try a real task on a
  fork under `agents/`.
