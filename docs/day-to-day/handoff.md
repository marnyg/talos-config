# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-07-29 (evening) — Mesh v2 gate spike **passed** (task closed,
uuid 69138146). Embedded `nebula/service` on a scratch fly app:
`listen.host: fly-global-services` works natively (nebula resolves
hostnames — no custom bind code); handshake 22ms; hub→peer netstack
dial 39ms; relay verified with a UDP-locked docker peer (`(relayed)`
handshake, 0% loss). ADR-0002 promoted to **Accepted** (evidence in its
Confirmation section). Spike app + dedicated IP destroyed; spike/
directory deleted after the verdict was recorded. Cleanup: committed
the outstanding #30 apply/tunnel work and the docs scaffold; recorded
ADR-0003 (tunnel source IP as authentication, Accepted); closed the
stale wg-spike handover task.

## Loose threads

- DNS: stock lighthouse `serve_dns` can't work on the TUN-less hub
  (verified). Phase 1 ports the wgdns pattern to the nebula netstack —
  needs a small variant of the TCP-only `nebula/service` package.
  Mobile-app DNS push unverified (fold into kill-criterion-4 check).
- Cert version pin: `nebula-cert` ≥1.10 emits V2 certs; nebula ≤1.9
  can't parse them. Node extension + clients must be ≥1.10 (hub embeds
  1.11.0).
- Revocation/expiry policy (uuid dc04e3e8) before enrolling
  shared-space devices.
- Legacy `docs/handover.md` still to be absorbed into day-to-day/.

## Suggested next steps

- Continue phase 1 (uuid fca5be68 — note: `1afafb50` is phase *2*):
  hub embedding, then factory schematic + nebula extension,
  compose-time injection, nebup enrollment, dual overlay, ≥1wk
  dogfood (kill criteria 2–4).
