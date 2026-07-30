# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-07-30 (office) — **Kill criterion 2 resolved: amended, not fired
(ADR-0006).** Phase 1's last open gate is now the dogfood window.

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

- **Criterion 2 no longer gates phase 2.** Remaining gates: the ≥1wk
  dogfood and certSANs (phase 2 step 1). Invariant 5's dual-overlay
  exception can close once phase 2 lands — it is no longer waiting on a
  punch verdict.
- **Criterion 4 (mobile/TV UX) still open**, behind the revocation policy
  thread (uuid dc04e3e8).
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
- Continue the dogfood to the ≥1wk mark, then phase 2 starting with
  certSANs.
- Settle revocation policy (dc04e3e8) to unblock the TV path.
