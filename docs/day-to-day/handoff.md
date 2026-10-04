# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-05: **ADR-0030 proven end-to-end.** A `talos/`-only signed
commit (`a76a828`, a comment in `mesh-blocklist-v3.txt`) pushed at
13:10:02Z; `POST /git/nudge` → 202; hub log
`git: serving main@a76a82862e41 (signed by marnyg@proton.me)` at
13:10:08Z. Six seconds push→served, image still `3896837`, no restart.
The unnudged poll path had already shown itself: `0859b5d` was picked
up at 13:04:09Z, one poll after boot. `/status` row confirmed by the
owner. Hub was unsealed by the owner before the check.

- `config-server/gitsync.go`: nudged syncs now floor at `nudgeGap`
  (10 s) after the previous attempt — the channel coalesced a burst
  into one kick but a steady unauthenticated stream still cost one
  `ls-remote` per sync. Not yet deployed (hub code change; rides the
  next deploy — behaviour on the running hub is the old one-kick
  coalescing, which is fine).
- `.beads.gate.lock` gitignored (0-byte beads-named lock at the root).
- `4iob` (retire the old docker host) closed as done by the owner.

## Loose threads

- `fly ssh console` is not provisioned on the nixos box (no fly ssh
  key issued) — observing the served tree directly needs
  `fly ssh issue` first; the `git: serving` log line is the
  authoritative post-swap signal anyway.
- The darwin laptop still needs the four `git config` signing lines
  (AGENTS.md "Git hooks") before it can push to `main`.
- `dsuj` still waits on the data copy off the Windows PC; `ch74`'s
  share-vs-sync question untouched.

## Suggested next steps

- Owner's pick from the board: spikes `dsuj` / `ch74` / `9z4e` /
  `kanr`, or disk-secret hygiene `spvd` / `9af0`.
- Next hub deploy carries the `nudgeGap` change; nothing urgent forces
  one.
