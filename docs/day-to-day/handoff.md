# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-04 (night): **the hub serves `talos/` from the signed git tip**
(spike `r4fw` → `lehx` `ra8k` `k9h7` `bfgz`; **ADR-0030**). **Deployed
and proven live** 13:01Z: `git: serving main@3896837b302e (signed by
marnyg@proton.me)` one second after boot. Two field fixes on the way:
the image had no CA bundle (`x509: unknown authority` — fail-closed held
the baked tree, as designed; `cacert` added to `fly/image.nix`) and
`DefaultPaths` resolved against the repo root, not `talos/` (now
`--git-subdir`, default `talos`, plus a "no served path" refusal).

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

- The hub was **re-sealed by the last deploy** (3896837) — sign both
  proposals at `/status` if not yet done. First `talos/`-only change on
  `main` after that is the end-to-end proof (served within 3 min, no
  deploy).
- The GitHub push webhook (`https://marnyg-talos-config.fly.dev/git/nudge`)
  is optional and unregistered; polling at 3 min suffices.
- `dsuj` still waits on the data copy off the Windows PC; `ch74`'s
  share-vs-sync question untouched.

## Suggested next steps

- Make a `talos/`-only signed commit and watch the `/status` row move
  without a deploy.
- Run the laptop's `git config` lines (AGENTS.md) before pushing from it.
- `4iob` (retire the docker host) is the next app-side item.
