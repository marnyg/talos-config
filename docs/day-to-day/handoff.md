# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-06 — **swarm batch 1 landed** (`35c`): five bd issues run as
worker agents in `~/git/swarm/<id>` worktrees, reviewed and
fast-forwarded to `main`. The orchestrator session was interrupted
mid-batch and resumed; nothing was lost.

- **`k3o`** — monorepo restructure: `protocol/` is its own Go module
  with a docs sub-scope; ADR-0020. Landed `65c2f83`.
- **`cmi`** — `.github/workflows/verify.yml` gates `go test` + the
  Quint models; quint pinned to the flake's 0.30.0. `75940cf`, `2cf1aa5`.
- **`359.1.4`** — P0.4 iroh API-churn probe: **PASS-WITH-CONDITION**.
  iroh is 1.1.0 under semver; breaking rate down ~10×; the condition is
  *own the Go binding* (Go is community-only). `docs/mesh-v3-iroh.md`.
- **`0bc.1`** — `protocol/cert` (primitive, JCS canonical JSON, Ed25519
  + EIP-191, strict decode, `Authorize`, `Attenuate`) and
  `protocol/clock` (`Mark`). Rapid ports of all 13 `authorize.qnt` and
  4 `clock.qnt` laws over real signatures. Two review rounds: round 2
  fixed **mark poisoning** (`Result.Verified` now lists only certs
  rooted at the receiver — ADR-0019/glossary amended on `main`,
  `57049aa`; model side is `zev`) and made `resolve()` scan all
  matching speak-as (re-unseal without redeploy). `952eeff`, `1dc4d20`.
  Follow-up `44r`: re-port the 5 hand-written speak-as tests from the
  model now that `czi` has landed.
- **`czi` + `jp2`** — run twice (opus vs fable) to pick the best;
  **fable's landed** (`09ae2d5`, `1081fa8`): 8 new speak-as laws, 15
  faults in `genNear`, 24 + 16 mutants tabled. The opus attempt is kept
  on branch `swarm/models` for reference — it encodes the hole below.

**Model-first findings from `czi`/`jp2` (ruling needed, all filed):**
- **`9l3` (P1 bug)** — "compare RESOLVED issuers" read as set overlap
  is unsound: any wallet can self-sign a speak-as for any hub key, so
  ROGUE→HUB_A + ROGUE→HUB_B makes OWNER2's member reach OWNER1's
  `admins` facet. Sound rule: **one consented sovereign vouches for
  both the grant's signer and the member's signer**
  (`invGroupMatchRootedInChain`, m14). The Go `Authorize` rejects this
  bundle today only because it resolves each cert to a *single* issuer
  and compares equality — align it with the ruled wording.
- **`xfx` (P1 bug)** — renewal at ⅔ life is insufficient under
  ADR-0018: a cert signed by a dead process carries that process's
  speak-as expiry; redeploy at speak-as day 90+ with a <60 d cert ⇒
  access lost at starvation 0. Renew on the first beat after the hub
  key changed (or schedule off *effective* expiry).
- **`q8h` (thread)** — the number: max unattended hub-process life is
  **90 d** (`SPEAKAS_LIFE − MEMBER_RUNWAY`); **120 d / 30 d-nag holds
  with zero margin** and *only if* `/sealed` at <30 d **stops serving
  beats**. Advisory-only nag breaks the 30 d promise past day 90. Also:
  after a missed nag, unseal→beat→deploy rescues certs; deploy-first
  strands them.
- **`zpf` (thread)** — grant-side `cav.groups ∋ g` on the speak-as for
  hot-key-signed group grants: modelled yes, brief said member only.

## Loose threads

- `zev`: `clock.qnt` should model untrusted issuers never advancing
  `lw` (the Go side already does this).
- `44r`: Go speak-as tests are ahead of their oracle until re-ported;
  and `9l3`'s ruling may change `resolve()`.
- Broken windows from `models-b` report: `runway.qnt`
  `invPolicyPropagates` lets mutant o5 (deploy fails to bake
  `gitEpoch`) survive; `authorize.qnt` step (1) fail-closed is backed
  by a `Map.get` runtime error, not a `Reject`; `check.sh` header says
  "Three tiers" and lists two.
- Boot-token HMAC key source (`54n`), ADR-0017 still Proposed until
  `359.8.1`/`359.8.5`, ADR-0019 node-agent NTP gate → `359.1.3` — all
  carried unchanged.
- Stale worker `pi` sessions may still be open in terminal panes
  (their worktrees are removed).

## Suggested next steps

- Rule `9l3`, `xfx`, `q8h`, `zpf` — they gate the glossary/ADR-0018
  wording that `0bc.2`, `359.8.1` and `44r` build against. `9l3` first.
- Batch 2 = Phase 0 probes `359.1.1–.3` once fly scratch + an Android
  device exist; then the gate `359.1.5`.
- `0bc.2` (two actors, envelope + runtime) is unblocked by `0bc.1`.
