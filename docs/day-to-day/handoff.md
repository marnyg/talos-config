# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-10-19, the task layer's first real run — and `pd5c`)

- **Verified `kmlg` from the parent's store** (debug container,
  `/proc/1/root/var/lib/sap-parent/sessions/*.jsonl`): the task layer
  ran end-to-end — project, task, `framed` → `#spawn` → born →
  `#task` → events → session stored → retry → `needs-human` — but
  every model call died on **OpenRouter `402 billing_error`** (balance
  below the 128k `max_tokens`). Three attempts in ~4 s per task, the
  board saying only "ended without a transition block". ADR-0043 is
  **not** decided yet: the framer has still never answered.
- **`pd5c`, the retry half** (`5c61830`, `1a38490`; deployed
  `f3b89ba`): `Store.LastAssistant` returns `RunEnd{Text, StopReason,
  Error}`; a run whose model call errored is reported as `model error:
  <pi's errorMessage>`; a refusal no retry can change (`40[1-4]`,
  `billing_error`, `authentication_error`, …) parks the task at once as
  `not retried, …` with attempts untouched; anything else retries after
  `Options.Backoff` (`-backoff`, 1 min doubling, cap 16×) — the beat's
  `reconcileTasks` is the retry clock (`retryDue` from `updated`, no
  schema change). `-retries` is a flag now; `park()` with no exit to
  `stuck` stamps the reason on the task (`Store.Stall`).
- Tests: fake pi learned `fake:error=<status>`; `TestTaskModelErrors`,
  `TestTaskRetryBackoff` (`export_test.go` `AgeTask`). Gates green:
  `go test ./...`, `nix build .#actors-bin`. `sap-parent` on `1a38490`
  (Argo needed a manual refresh annotation to pick the pin up).

## Loose threads

- **OpenRouter has no credits.** Nothing in the task layer can run
  until it is topped up; the first run after will show the new
  `not retried, model error: 402 …` note if it is not.
- `pd5c`'s other edges are open: transition as a typed stream
  event/tool, `tools[]` per persona, per-project credential sets.
- `git push` takes ~4–5 min: `.githooks/pre-push` triggers the full
  vendorHash dance (4 attrs × 2 builds, `--rebuild` re-fetches
  modules) on any `actors/**/*.go` change, though `actors/` is never
  vendored. Fix proposed, not applied — see notes 2026-10-19.
- `actors/build.sh` once evaluated a stale rev (tagged and re-pushed
  the old `4edbd93` image byte-for-byte); the second run was right.
  Check the "building …:<tag>" line against `git rev-parse --short HEAD`.
- Carried: rotation never exercised live (`l3on`); `bsj` (Longhorn
  backup) blocks real task-layer use; `tmm5`, `88uq`, ntfy first page.

## Suggested next steps

- Top up OpenRouter, re-run the framer on a throwaway project
  (`kmlg`), then decide ADR-0043.
- Narrow the pre-push hook's triggers (notes 2026-10-19).
- `bsj`.
