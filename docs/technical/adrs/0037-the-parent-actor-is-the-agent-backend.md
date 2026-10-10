# ADR-0037: The parent actor is the agent backend — sessions in its own store, pi driven over RPC, results as Forgejo branches

- Status: Proposed (designed 2026-10-11 in a grill-design session;
  nothing built). Epic `talos-config-ivof`; the build is `e6pu` plus
  `ivof.1`/`ivof.2`.
- Date: 2026-10-11
- Related: ADR-0035 (the sandbox layer), ADR-0036 (the null option
  this grows out of; its deferred "protocol facet" and "wallet-gated
  surface" land here), ADR-0032 (the person gate the UI sits behind),
  decision `6d0u` (no kagent/ax), decision `4la8` (`talos-config` stays
  on GitHub), protocol ADR-0005 (consumer-driven additions), protocol
  ADR-0009 (provisioner is an actor), protocol invariants 1, 7, 12,
  13; `bsj` (backup target — now a dependency), thread `264f`
  (volumes on the lease, deferred), spike `7ymy` (person binding).

## Context and Problem Statement

ADR-0036 ran the first agent as a plain Job: prompt in, transcript out,
`tmux attach` to watch. Real use asks for four things it cannot give:
a **workspace** that is a git repo the agent can push to, a **result**
that survives the pod and can be reviewed, a **session** that can be
watched live from a phone, searched across runs, resumed, and shared
with other people, and a way to **steer** a run mid-flight. The
question was where that state and that UI live — and whether building
them reopens the orchestrator question `6d0u` closed.

## Decision Drivers

- Protocol invariant 12: delivery is at-most-once; "the only reliable
  message is the reply". A reply that carries the only copy of a
  result is a result that can be lost.
- Protocol invariant 12 again, and the provisioner's lease table:
  actors restart from spec; **durable state is a data-plane resource**
  outside the actor, and in-memory tables are caches (recorded in the
  protocol domain-model this session).
- Protocol invariant 1 / the Kit's shape: the starter kit is chains
  and locations. A bearer token in it would be a second authority
  primitive.
- ADR-0032: per-user state goes behind the person gate; bare headers
  are never trusted.
- ADR-0005: no speculative protocol fields; additions follow a
  consumer.
- `focus.md`: let real use decide what hurts first — and it did: watch
  live and search are the asks.
- pi ships an **RPC mode** (`pi --mode rpc`: JSONL commands in,
  session events out, `steer`/`abort`, close-stdin shutdown).

## Considered Options

### Where the git credential lives

- **Provisioner policy keyed on image** — chosen. The driver attaches
  `{openrouter, forgejo-agent}` to `sandbox-agent` pods; `#spawn`
  stays `{image, params, until}`; `#task` carries names (`repo`,
  `ref`, `prompt`), never secrets. Scope = the token's scope: v0 is a
  bot user in the `agents` org, so "a child cannot be re-pointed" holds
  per org, not per repo.
- Token in the starter kit — ruled out: second authority primitive;
  the parent would hold git credentials for every child.
- Token in the `#task` envelope — ruled out: parent as token
  distributor; secret in a signed body that leaks into transcripts.
- Parent names a secret in `#spawn` — ruled out: reads any secret in
  `ai` through a child.
- Forgejo deploy keys minted by the child (invariant 7) — deferred,
  not ruled out: the per-repo endgame; needs a Forgejo-aware parent.

### What the result is

- **A Forgejo branch + PR; the swarm-worker report as the PR body; the
  `#task` reply is a pointer `{status, branch, head, pr}`** — chosen.
  Humans merge, in Forgejo for `agents/*` repos, on the laptop
  (signed) for anything bound for GitHub.
- Report inline in the reply — ruled out (invariant 12).
- Report as a file on the branch — ruled out (pollutes review).

### Where sessions live

- **The parent actor** — chosen: spawner + store + UI are one thing.
  Store = SQLite with FTS5 on a Longhorn PVC the parent's manifest
  declares; the born table stays a cache re-adopted from the
  provisioner's `List` and the store. First actor with a durable
  datastore of its own ⇒ `bsj` blocks the epic.
- Git as the session store (`agents/sessions`, push on timer) —
  superseded in-session: durable and resumable, but streams nothing
  and indexes nothing.
- A standalone session service — ruled out: the parent's born table
  with more columns; the ingest boundary would be built twice.
- Postgres — ruled out: single writer, single owner, no HA anywhere
  else (`4ze8`).
- A storage-provider actor ("bring your own provider" for state) —
  ruled out: BYO provider already *is* the compute design (invariant
  13, two drivers, payment deferred to M5 by being our own provider);
  a volume has nothing to hand over the mesh, so storage is a
  dimension of the lease — thread `264f`, deferred under ADR-0005.
- kagent / ax UI and Postgres — ruled out by `6d0u`; they manage their
  own agents, not pi.

### How events get out of the sandbox

- **The child beat runs pi as an RPC subprocess** — chosen. Prompt
  down as a `prompt` command, every record up over a stream facet to
  the parent, `steer`/`abort` down, deadline/`#kill`/lapse = close
  stdin. Two artifacts: the event stream (live, search) and pi's own
  session JSONL shipped at settle (resume/fork).
- A pi extension pushing to a socket (herdr's pattern) — ruled out:
  observe-only; control would need a second channel; runs inside the
  sandboxed process.
- Tailing the session file — ruled out: racy, unstructured, no
  control.

### Who may see and steer

- **Person gate; owner = the submitting wallet; share list of wallets
  or groups at two rights, `view` and `steer`; `admins` views all;
  nothing public; enforced in the parent** — chosen. Steers carry the
  sender's wallet into the transcript.
- Group gate + ACL by `X-Mesh-Groups` — ruled out by ADR-0032.
- Device-level sharing — ruled out; `7ymy` makes it moot.
- Finer rights — ruled out: the orchestrator question again.

## Decision Outcome

The agent backend is the **parent actor**: it spawns agents as
children (`e6pu`), attaches nothing but a lease and a kit, receives
each child's pi event stream over a stream facet, keeps sessions in
its own store, and serves the list/watch/steer/search UI behind the
person gate. Results are **Forgejo branches and PRs**; the protocol
reply is a pointer. Credentials are **provisioner policy**; the wire
carries names. Forgejo (`ivof.1`) hosts agent work and mirrors;
`talos-config` stays on GitHub (`4la8`).

This is not the orchestrator `6d0u` rejected: no third-party
reconciliation, no CRDs. A parent supervising children by lease is
what the protocol is; the "backend" is that parent with a volume.

### Consequences

- `e6pu` grows: driver `RuntimeClass` **and** per-image secret sets;
  the child beat gains a pi RPC client and a stream-facet uploader;
  `#task` is `{repo, ref, prompt}` → pointer.
- The parent is a new long-lived actor declared in git (Deployment in
  `ai`, PVC, OIDC client of `siwe-oidc`). Its backup is `bsj`.
- Root invariant 2, actor-owned state: "an actor with an ephemeral key
  holds no durable state" \u2014 so the parent is a **durable-key** actor
  (key and Kit on its own volume, like the in-cluster gateway), not a
  hub-style ephemeral one; its children's edges survive its restart by
  re-adoption, not by re-birth. Sessions and share lists are
  data-plane bookkeeping about payload the parent holds (invariant 2's
  stated exception); if share lists ever become *policy* (groups,
  defaults), they move to git like ArgoCD's RBAC. Transcripts can
  contain whatever the agent read, so the volume inherits `9io`
  (encrypting Longhorn user volumes).
- The `tmux attach` path survives as a debug door only; "watch" is the
  parent's UI, and a terminal client of the parent's stream is the
  power-user path later.
- Egress and budget stay unbounded (ADR-0036's gaps); budget becomes a
  protocol caveat only at M5.
- `scripts/agent-run.sh` (`ivof.2`) gets the same workspace/result
  shape with no protocol — the interim until the parent exists.

### Confirmation

A task submitted from the phone shows up in the list, streams live,
takes a `steer` from a shared wallet, ends as a PR on `git.gw`, and is
resumable from its shipped session file after the pod is gone. A
non-owner, non-shared wallet gets 403 on the session. A restarted
parent re-adopts running children and shows their sessions.
