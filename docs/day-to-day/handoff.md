# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-10: **the first agent ran under kata** — `tj7c`, ADR-0035's
"null option", as a plain workload with no orchestrator.

- `k8s/apps/sandbox/image.nix` → `ghcr.io/marnyg/sandbox-agent:9da5725`:
  pi 1.0.4 (own flake lock entry `nixpkgs-pi`; the repo's nixpkgs
  predates the package), tmux, git/rg/fd/jq, uid 65534, read-only
  root. `AGENT_PROMPT` set ⇒ `pi -p` batch; unset ⇒ pi in a detached
  tmux session, attached with `kubectl exec -it … -- tmux attach`.
- `k8s/apps/sandbox/agent.yaml`: suspended CronJob `agent` in `ai` as
  the pod template (`runtimeClassName: kata`, `envFrom` the sealed
  `openrouter` key, 4 h deadline, emptyDir workspace).
  `scripts/agent-run.sh` renders a Job from it (`-f`, `-m`, `-d`,
  `-q`). Entry = the kubeconfig over the mesh; no new HTTP surface.
- Live on `talos-wu6-eib`: batch `agent-first` answered
  `6.18.5, uid=65534(nobody), sandboxed` (guest kernel; host 6.18.18);
  interactive `agent-tty` came up with the TUI in tmux.
- Two things bit: kata's never-closing stdin pipe hung `pi -p` until
  `</dev/null` (9da5725), and a hand `kubectl apply` of the pin was
  reverted by selfHeal before the test Job ran (notes 2026-10-10).

## Loose threads

- `tj7c` is closed (ADR-0036 Proposed — the owner flips it to
  Accepted). The agent-as-child follow-up is `e6pu` (driver
  `RuntimeClass`, `child` beside pi, a `#task` facet).
- Egress from the sandbox is unbounded (no NetworkPolicy enforcement on
  flannel); the OpenRouter key's own spend limit is the only budget
  bound (`0bc.5`). Both stated in `agent.yaml`, neither filed.
- `kata-qemu` handler is on the nodes, not declared in git.
- Standing: herdr 0.9.1 vs 0.8.2, `jlgz`, `bsj`, `bhui`; `<!-- stale?
  -->` flags in `notes.md` (now 9) await the owner.

## Suggested next steps

- Use it: a real task through `scripts/agent-run.sh` to see whether the
  virtio-fs cost (`5h0j`) or the emptyDir workspace is the first thing
  that hurts.
- `e6pu` (agent as child) when ready to design it.
- `bsj` backup target; `dsuj` still waits on the Windows data copy.
