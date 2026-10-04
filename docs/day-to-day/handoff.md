# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-04 (night): **the hub serves `talos/` from the signed git tip**
(spike `r4fw` → `lehx` `ra8k` `k9h7` `bfgz`; **ADR-0030**). Code is on
`main` (`9bbd1ec`…), **not yet deployed** — the live hub still serves
its baked snapshot.

- **Commit signing is on** (`lehx`): `talos/allowed-signers` holds the
  owner's `id_ed25519` pub; `.githooks/pre-push` refuses an unsigned
  tip (`SKIP_SIGNED=1`); per-clone setup in AGENTS.md "Git hooks".
  Done on the nixos box; **the darwin laptop still needs the four
  `git config` lines** before it can push.
- **`config-server/gitsrc/`** (`ra8k`): go-git `ls-remote` + shallow
  clone, SSHSIG verify of the tip against the signers (pure Go,
  `hiddeco/sshsig`; `TestVerifyRealHead` proves it reads what
  `git commit -S` writes), sparse materialize, atomic symlink swap,
  prune. vendorHash bumped.
- **Wiring** (`k9h7`): `--git-remote/--git-ref/--git-poll`; `--root` is
  a symlink (`/dev/shm/hub/talos` → `talos-baked` at boot);
  `POST /git/nudge`; `/status` row "talos/ from git" with a branch
  override form (volatile — restart reverts to `main`); new trees are
  decrypted with the held master before and after the swap;
  `composeFor` pins one tree per request. `fly.toml` sets
  `GIT_REMOTE`/`GIT_REF`.
- Earlier the same day: `files` share stays up by decision; domain
  model gained the "user files" data class; stale `vzbf`/`lwi3`/`hwtp`
  references cleaned.

## Loose threads

- **First deploy of ADR-0030 is the real test**: `HUB_BUILDER=mar@nixos
  fly/deploy.sh`, unseal, then watch the "talos/ from git" row flip from
  "baked image tree" to `main@<sha> signed by marnyg@proton.me`. If it
  stays on baked, the row shows the fetch/verify error. The fly VM
  needs outbound HTTPS to github.com (it already reaches fly's
  registry; nothing in `fly.toml` blocks egress).
- The GitHub push webhook (`https://marnyg-talos-config.fly.dev/git/nudge`)
  is optional and unregistered; polling at 3 min suffices.
- `dsuj` still waits on the data copy off the Windows PC; `ch74`'s
  share-vs-sync question untouched.

## Suggested next steps

- Deploy and verify ADR-0030 live (above); then a one-line `patch.yaml`
  edit on `main` should be served within 3 min with no deploy.
- Run the laptop's `git config` lines (AGENTS.md) before pushing from it.
- `4iob` (retire the docker host) is the next app-side item.
