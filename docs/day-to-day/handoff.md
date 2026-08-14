# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-08-14 (late sitting) — **ADR-0012 slice reviewed, hardened,
deployed, owner-verified.**

- Pre-deploy review of the uncommitted slice found five issues, all
  fixed: nebup's key-splice deleted every newline before `pki.key`
  (would have corrupted the first real config — caught by review, now
  unit-tested); the /status mesh-enroll approval card did not exist
  (the RFC 8628 server endpoints were unreachable from any UI — card
  with editable name/group, live-rebuilt v1 message and admins-retype
  landed, plus render test); `/verify` could "approve" a mesh
  enrollment into a payload-less token (now refused; deny still
  works); the enrollment audit log fired before the nonce/approval
  commit point (moved after); the device-flow path had zero test
  coverage after `nebtv_test.go`'s deletion (now e2e-tested including
  the retype gate and single-use config redemption).
- Committed as `b7fc3b5` + `8943046`, deployed to fly. Owner
  smoke-tested end to end: unseal → `nebup -rekey` → tunnel →
  `/config` admins gate → device-flow approval through the new card.
  Deploy task `d3c8f514` closed.
- **Mobile Nebula import assumption VERIFIED** (owner-tested): the
  stock app works given the hub yaml. Phones need no custom client.
  ADR-0012's consequences updated; TV APK task `2e1bef85` annotated —
  its scope shrinks to "does the TV need a leanback UI, or can it
  sideload Mobile Nebula".

## Loose threads

- **w1 still down** (since 2026-08-04) — gates the entire storage arc;
  3 `longhorn-bulk` volumes `faulted`, VM volumes `degraded` 1-of-2.
- **90-day renewal**: the laptop's fresh cert expires ~2026-11-12.
  Automation (`49443c38`) is now cheap — `nebup -reenroll` re-signs the
  same key — but needs a non-interactive signing story before a cron
  can run it.
- Old master-derived device certs (office MacBook enrolled as
  `laptop`, phone) stay **valid until their 90-day expiry** — same CA.
  Blocklist by fingerprint if simultaneous use matters before then.
- Review leftovers, not yet filed: "denyd" log typo (`oauth.go:180`),
  stale static-wins comment on `dnsRespond` (`nebdns.go`),
  `/mesh/enroll/challenge` accepts empty names.

## Suggested next steps

- Power-cycle w1; verify `longhorn-bulk` leaves `faulted` — this opens
  the storage arc (backup target `8b9972fd`, replica raise `da61bd8e`).
- Filler while w1 is down: the etcd advertised-subnets bug (`6c456522`).
