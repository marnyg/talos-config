# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-06 — **spike `7vv` ruled: unseal becomes a `speak-as`
delegation; the master shrinks to a secrets seed.** Docs only, no
code. Record: **ADR-0018** (Proposed), decision `89v`, notes on `7vv`.

- Framing that unlocked it: the master roots two unlike things —
  *authority* (CA/issuer/hub key) and *secret material* (age seed,
  KMS seal keys, recovery passphrases). `speak-as` replaces only the
  first. So 7vv is a split, not a replacement; the phishable frozen-
  message signature shrinks to "decrypt secrets / unlock disks" and
  becomes rotatable. The hub is exactly the protocol sketch's *hot
  key for a cold root* (§Identity) — nothing new was invented.
- Three consequences the ticket lacked, now in ADR-0018 and the
  glossary: (1) `speak-as` must **resolve issuer before compare** or
  per-process hub keys break mixed-epoch bundles (group rule, step
  2b); (2) **verification-time validity** ⇒ member runway is
  `min(member.exp, speakas.exp) − last_beat`, and the `speak-as` is
  per *process* — hence 120 d lifetime + `/sealed` nag at < 30 d;
  (3) caveats are literal (verbs, groups, `delegable:false`) — never
  "MACs in git", verifiers don't read git.
- **Invariant 1 amended**: "wallet-derived issuer keys" → "wallet-
  rooted" (derived *or* delegated); "re-derivable: CA" → "hub
  authority". **Invariant 2 gained the actor-owned-state clause**:
  state is encrypted to the actor's own durable key or re-derivable
  from git + a startup delegation; ephemeral-key actors hold none.
- Domain model §2 redrawn (wallet → speak-as → hubkey → certs; seed
  → secrets only), depth two → three; glossary *Hub*, *Unseal*,
  *Speak-as* (new), *Authorize* step 2a, *Cert classes*, *KMS*.
- Derived issues: `861` hub-as-actors spike (Issuer/Enroll/Relay/
  Gateway ephemeral-key; Provisioner alone holds a seed; modular
  monolith with per-actor inboxes first); `qrb` actor-owned-state
  spike, P1, carries the **open question: Provisioner seed wallet-
  derived vs fly-held**. Downstream notes left on `359.8.1`, `0bc.1`,
  `czi`, `jp2`, `k3o` (monorepo ADR is now 0019), `fbb`.

## Loose threads

- `7vv` still open pending the user's close signal (fully derived).
- `runway.qnt` (`jp2`) and `authorize.qnt` (`czi`) do not yet model
  the `speak-as` link; the 30 d member-runway claim (`z1z`) is
  unverified under the new bound until they do.
- ADR-0015's accepted cross-redeploy replay residual should
  *disappear* once the boot-token HMAC is keyed from `hubkey`;
  `check.sh` still asserts it stays — flip when `approval.qnt` is
  updated.
- `439` (time) gains a note-worthy angle: the hub's own `speak-as` is
  the one cert it can check against a fresh wallet act. Not recorded
  on `439` yet.
- Carried: ADR-0017 Proposed until `359.8.1`/`359.8.5`; the 2026-09-05
  inline amendments still not folded; `docs/mesh-v3-iroh.md`
  architecture block still says "HKDF master → issuer key" (stale vs
  ADR-0018 — fix when Phase 1 starts); `cmi` CI for `check.sh`.

## Suggested next steps

- Rule `qrb` (Provisioner seed location) — it is the last open input
  to ADR-0018 and to what `359.8.1` builds.
- Extend `authorize.qnt`/`runway.qnt` with the `speak-as` link per
  the notes on `czi`/`jp2`; that either confirms the 120 d / 30 d-nag
  numbers or refutes them before any code.
- `439` (time) is still the unruled structural gap ahead of `359.8.1`.
