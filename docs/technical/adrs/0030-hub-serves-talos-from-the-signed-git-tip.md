# ADR-0030: The hub serves `talos/` from the signed git tip, not the image

- Status: Accepted (2026-10-04)
- Date: 2026-10-04
- Amends: `fly/image.nix` / `agedecrypt.go` wording ("the image ships
  only `.age` ciphertext" — the image now ships a *fallback* tree; the
  served tree is fetched); README "Deploying the hub" (a deploy ships
  hub code, not fleet config). ADR-0018's posture (secrets decrypt at
  unseal, into tmpfs) is unchanged.
- Related: invariant 2 (git is compiler input), invariant 3 (GitHub is
  not a root of trust), invariant 8 (plaintext only in tmpfs),
  ADR-0019 (safe-to-lose caches), spike `talos-config-r4fw`, tasks
  `lehx` `ra8k` `k9h7`

## Context and Problem Statement

The hub read `talos/` from `--root` on every request — `machines.Load`,
`BuildConfig`, `policy.Load`, the blocklist — so it already "resolved
at runtime". But the tree under `--root` was a snapshot taken when the
fly image was built. Changing a `patch.yaml`, a machine's role, the
mesh policy or the blocklist cost `fly/deploy.sh` (an x86_64 nix build,
a registry push, `fly deploy`) plus the re-seal every deploy causes and
two wallet signatures (`fbb`). `main` routinely ran ahead of what the
hub served, and `/status` showed an image tag, not a served commit.

The blocklist is the sharpest case: revocation latency for a member was
"until someone deploys", for a change that is one line in git.

## Decision Drivers

- Invariant 2 says git is compiler input. The hub is the compiler, so
  it should read git — the one thing it was not doing.
- Invariant 3: GitHub is a transport, not a root of trust. Whatever the
  hub fetches must be verified against a key the owner holds.
- Invariant 8: nothing new on disk; the fetched tree and its plaintext
  live where the baked tree did (tmpfs).
- One act per change. Merging to `main` should be the deploy.
- Fail closed to *something*: a fetch or verification failure must
  leave a working hub, not an empty tree.

## Considered Options

### Option A: Keep baking; make deploys cheaper

Faster image builds, CI-driven deploys on every push to `main`.

- Pros: no new trust statement.
- Cons: every config change still re-seals the hub and needs two
  signatures; `main` still runs ahead between pushes and deploys;
  revocation latency is still "a deploy".

### Option B: Fetch `main` over HTTPS, trust the transport

Poll GitHub, serve the tip.

- Pros: smallest change.
- Cons: a GitHub account compromise is then a fleet-config compromise.
  Violates invariant 3 outright.

### Option C: Fetch a ref, serve only a tip ssh-signed by the owner key (chosen)

The hub polls a ref (default `main`), fetches its tip when it moves,
and serves it only if the commit's SSHSIG verifies against the keys in
`talos/allowed-signers` — read once at startup from the **baked** tree,
never from the fetched one. The owner's `~/.ssh/id_ed25519` is the key
in that file: the same root as invariant 3 and the age recipient for
`talosconfig.age`. The tip's signature attests the whole tree, so only
the tip is checked.

- Pros: a signed merge to `main` is the deploy; the transport is
  untrusted; the pinned key is the owner's existing root; no deploy and
  no re-seal for config changes; revocation is one signed push.
- Cons: every commit on the served ref's tip must be signed by the
  owner — no GitHub-UI merges (signed by GitHub's key), and every clone
  needs `gpg.format ssh` + `commit.gpgsign`. A `.githooks/pre-push`
  check refuses an unsigned tip so the hub never silently stays behind.

### Option D: A separate "serve" ref the owner moves

A `hub/serve` branch or tag as the explicit deploy act.

- Pros: "merged" and "served" stay distinct.
- Cons: re-introduces the second act Option C removes; the signature
  requirement already provides the gate. Kept as a runtime *override*
  instead (below), not as the default.

## Decision

Option C. Mechanically (`config-server/gitsrc`, `gitsync.go`):

- `--root` is a **symlink** the hub re-points. `fly/entrypoint.sh`
  stages the image's tree at `/dev/shm/hub/talos-baked` and links
  `/dev/shm/hub/talos` to it; `GIT_REMOTE`/`GIT_REF` in `fly.toml` turn
  fetching on. A hub without `--git-remote` serves `--root` as before.
- Poll (`--git-poll`, 3 min) is an `ls-remote`; a shallow in-memory
  clone happens only when the ref moved. `POST /git/nudge` is an
  unauthenticated "check now" for a GitHub webhook — the content is
  verified, not the caller.
- A new tree is materialized beside the link (`talos-<sha>`), sparse
  (`machines/ clusters/ base/ hardware/ mesh-*-v3.* age-recipient.txt
  allowed-signers`; symlinks and submodules refused), decrypted with the
  held master **before** the swap (an undecryptable tree is not served)
  and once more, idempotently, **after** it (closes the race with an
  unseal that decrypted the previous tree in between). While sealed the
  swap still happens; the unseal decrypts whatever the link points at.
- The swap is atomic (symlink + rename). `composeFor` resolves the link
  once per request so one config is built from one tree. The previous
  tree is kept until the next swap; older ones are pruned.
- Any failure — fetch, verification, decrypt — leaves the link where
  it was and shows on `/status` ("talos/ from git" row: served
  `ref@sha`, signer, commit age, last error).
- **Volatile override**: `/status` (session-authenticated) can switch
  the served ref to a branch for an experiment. It is a safe-to-lose
  setting in ADR-0019's sense: a restart (so any re-seal) reverts to
  `main`. The signature requirement applies to the branch tip too.

What a deploy is now: hub **code**. `fly/deploy.sh` is unchanged;
`talos/` edits no longer need it.

## Consequences

- Config changes are live within a poll of a signed push, no re-seal.
  The blocklist's revocation latency drops from "a deploy" to ~3 min.
- A new trust statement, now explicit: *a commit signed by a key in
  `talos/allowed-signers` is servable.* Rotating that key is a deploy
  (the file is read from the baked tree), like `age-recipient.txt`.
- Commit signing is mandatory on the served ref (AGENTS.md "Git hooks",
  `talos-config-lehx`). Collaborators would need their keys in
  `allowed-signers` — out of scope; there are none.
- The image still carries the tree it was built from. It is the first
  thing served after a (re)start until the first verified tip, and the
  fallback forever after. `/status` warns while that is the case.
- `fly/image.nix`'s "the image ships only `.age` ciphertext" remains
  true of what is on disk; plaintext still exists only in `/dev/shm`.
- Not done: webhook registration on the GitHub repo (manual, optional —
  polling suffices); serving the hub's own policy tests from the fetched
  tree (they run at build, against the baked files, as before).
