# ADR-0036: The first agent is a plain Job running pi, entered through the kubeconfig

- Status: Proposed — in effect 2026-10-10: `agent-first` (batch) and
  `agent-tty` (attached) ran under kata on `talos-wu6-eib`. Task
  `talos-config-tj7c`.
- Date: 2026-10-10
- Related: ADR-0035 (the sandbox layer this runs on; it named this as
  "the null option"), decision `talos-config-6d0u` (no kagent/ax until
  1.0 GA), spike `kanr` + `docs/spikes/agents-kagent.md` (the
  comparison), protocol ADR-0009 (children; the follow-up), invariant 5
  (single public entrypoint), `0bc.5` (budget as counted caveats)

## Context and Problem Statement

ADR-0035 put a guest kernel under any pod that asks for one and left
the orchestrator question open. The owner's need is concrete: run a
coding agent against a throwaway workspace, from the laptop, sometimes
watching it work. Three things had to be chosen: what the agent
*is* (which harness), how a run is *declared* in a GitOps repo whose
root Application syncs everything under `k8s/apps/` with selfHeal, and
how a run is *entered* — who may start one and attach to it.

## Decision Drivers

- The kanr memo's conclusion: for v0 an agent is a plain workload;
  "agent as child" is an experiment on top of the null option, not on
  an orchestrator.
- Invariant 5: no new public surface; every remote path is the hub.
- Invariant 1/goal "every exposed service authenticates against the
  wallet": a new entry must already be wallet-rooted or must not be a
  service.
- Invariant 2: the run's definition in git; nothing the pod remembers
  matters.
- The owner drives pi daily; a second harness is a second thing to
  learn and to patch.
- Watchability: an agent that runs code is something the owner wants
  to see mid-run, not only read a transcript of.

## Considered Options

### Harness

- **pi (`pi-coding-agent`)** — chosen. Speaks OpenRouter natively
  (`OPENROUTER_API_KEY`), has a print mode for batch and a TUI for
  attached use, is nix-packaged. Cost: the repo's nixpkgs predates the
  package, so pi rides its own flake lock entry (`nixpkgs-pi`) rather
  than bumping the nixpkgs every Go and Rust build pins to.
- A bare Go/Python agent loop — ruled out: re-implements tool calling,
  context management and a UI the owner already has.
- kagent / ax — ruled out by decision `6d0u` (ADR-0035 option E).

### Declaring a run

- **A suspended CronJob as the template** — chosen. ArgoCD owns a pod
  template (`suspend: true`, a schedule that never fires); each run is
  a Job rendered from it by `scripts/agent-run.sh` (jq over the
  template: prompt, model, deadline). The Job is not in git and is not
  meant to be: it is the run, the template is the definition.
- A Job manifest in git — ruled out: fires once at sync, then is
  immutable; every run would be a commit.
- `kubectl create job --from=cronjob/agent` — ruled out: cannot set
  the prompt (no env override), so a wrapper was needed anyway.
- An operator/CRD (`AgentRun`) — ruled out: the orchestrator question
  by another name; nothing here needs reconciliation.

### Entering a run

- **The kubeconfig over the mesh** — chosen. The API server's admin
  path is already wallet-rooted (talos credentials, `cp1.mesh.internal`
  SAN on the identity plane); starting a Job and `kubectl exec -it …
  -- tmux attach` reuse it. Nothing new listens anywhere.
- A wallet-gated HTTP surface (an Ingress behind the group gate, like
  SillyTavern) — deferred: it would be the right shape for a UI, but
  a batch/attach workflow has no UI, and a web terminal is a bigger
  attack surface than `exec`.
- A protocol facet (`#task` on a child) — deferred to the
  agent-as-child follow-up: the parent would invoke it after birth,
  the prompt would travel as a signed envelope, and the lease would be
  the deadline. That is the design the k8s driver already births Jobs
  for; it needs `runtimeClassName` in the driver and the child beat
  beside pi in the image.

### Attaching

- **tmux inside the pod** — chosen: `pi` runs in a detached session;
  `kubectl exec -it -- tmux attach` gives detach (`C-b d`), reattach,
  redraw and resize. `TMUX_TMPDIR` is image env so a bare `tmux
  attach` finds the socket.
- `kubectl attach -it` to a TTY container — ruled out: no detach key,
  a reconnect does not redraw a TUI.
- `pi --mode rpc` + port-forward — ruled out for v0: needs a client on
  the laptop; the TUI already is one.

## Decision Outcome

Chosen: **pi as a plain Job under kata, declared as a suspended CronJob
template, entered through the kubeconfig, attached through tmux.** It
is the smallest thing that is an agent the owner can start and watch;
every alternative adds a component whose need has not been shown.

### Consequences

- An agent run is one command on the laptop and one Job in the
  cluster; nothing else to operate.
- The entry gate is as strong as the kubeconfig — cluster-admin.
  Anyone who may start an agent may do anything; there is no finer
  grant until the protocol facet exists.
- The sandbox bounds the node, not the network: no NetworkPolicy
  enforcement on this CNI, so egress is unbounded. The OpenRouter key's
  spend limit is the only budget (`0bc.5`).
- The workspace dies with the Job; results leave as the log or as
  whatever the agent pushed. A durable workspace is a later choice
  (`5h0j` decides whether virtio-fs or a block volume).
- A kata pod's stdin is a pipe that never closes; any non-interactive
  program in it must read `</dev/null` (the first run hung on this).
- pi's version moves with `nix flake update nixpkgs-pi`, independent
  of the rest.

### Confirmation

A real task runs end to end from `scripts/agent-run.sh` and the owner
attaches to a live one. Invalidated if real use needs a UI, multiple
concurrent users, or per-run authority — then the HTTP gate or the
protocol facet is the next ADR, not this one amended.
