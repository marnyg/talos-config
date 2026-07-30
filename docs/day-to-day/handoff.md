# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-07-30 (office) — **Kill criterion 2 resolved: amended, not fired
(ADR-0006).** Phase 1's remaining gates are three concrete exit checks
and criterion 4.

- Office Wi-Fi punch test: **RELAYED**, and the run is **valid** —
  Tailscale was up but split-tunnel with no exit node, and `route get`
  for both cp1's WAN and fly showed egress on the physical `en0`.
- NAT classified at both ends by STUN (one socket → several destination
  IPs): home = endpoint-independent **and** port-preserving (cone);
  office = **symmetric, random ports**; cellular = CGNAT symmetric.
  Criterion 2's premise ("relay ⇒ home NAT is symmetric") is therefore
  **falsified** — home was never the blocker, and no overlay punches
  through two symmetric remote NATs.
- Criterion 2 restated as a parity-plus-LAN test in `mesh-v2-nebula.md`
  (canonical), cross-referenced from ADR-0002, recorded in ADR-0006.
  `goals.md` now scopes driver 1 to **LAN-direct**, with remote P2P an
  explicit non-goal.
- Established while comparing against Tailscale: making the home side
  reachable (UPnP/NAT-PMP/PCP or a static forward) **would** work — only
  one side needs reachability, after which the remote symmetric NAT is
  irrelevant. Rejected on **invariant 5**, not on capability. The earlier
  claim that a port forward "cannot fix" this was wrong and is corrected
  in ADR-0006.

## Loose threads

- **Criterion 2 no longer gates phase 2.** The ≥1wk dogfood window was
  replaced (2026-07-30) by three exit checks — node reboot, hub re-seal,
  roaming reconvergence — because calendar time was a weak proxy for the
  resilience events it stood for. None have been run yet; together they
  are about an hour. Plus certSANs (phase 2 step 1). Invariant 5's
  dual-overlay exception can close once phase 2 lands.
- **Criterion 4 (mobile/TV UX) still open**, behind the revocation policy
  thread (uuid dc04e3e8). It is deliberately *not* one of the exit
  checks: with driver 1 narrowed to LAN-only by ADR-0006, leaving
  criterion 4 unmeasured means phase 2 rests on LAN-direct plus
  consolidation alone. Defensible, but it should be an explicit call.
- **TV onboarding is built but NOT deployed** (`f2cc4b7`). Deploying
  re-seals the hub, so it needs an unseal immediately after — but it is
  no longer held back by a pending measurement.
- **The office MacBook now holds a `laptop` mesh credential**, the same
  derived identity as the home laptop (identity = f(master, name), and
  both used the default name). Same key, same overlay address — do not
  run nebula on both at once. Needs a decision (see next steps).
- New tasks filed: **42** (`+bug`, nebup overlay-underlay guard) and
  **43** (`+thread +later`, revisit direct remote paths if IPv6 lands).

## Suggested next steps

- Decide the office-mac credential question: leave as-is, revoke via
  `talos/mesh-blocklist.txt`, or re-enroll office machines under a
  distinct name with a restricted group.
- Run the three phase-1 exit checks (node reboot, hub re-seal, roaming),
  then phase 2 starting with certSANs. The hub re-seal check comes free
  with deploying the TV build.
- Decide criterion 4: measure the mobile/TV path, or formally drop
  driver 2 and record that phase 2 stands on LAN-direct + consolidation.
- Settle revocation policy (dc04e3e8) to unblock the TV path.
