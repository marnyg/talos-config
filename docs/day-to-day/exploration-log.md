# Exploration Log

<!-- "What we've tried and ruled out." Prevents re-attempting dead ends across sessions.
     Granularity: strategy-level pivots only. Not "used ripgrep instead of sed".
     Yes: "tried library X, ruled out for reason Y." -->

## Sovereign-actor networking design exploration (task 51 sketch)

- 2026-08-16 — Identity-handle branch: **chain-registry indirection
  ruled out** for the identity layer (chain read in enrollment/auth
  path; identity lifetime coupled to one chain's liveness). Landed
  on: handle = cold root key's address; hot networking keys via
  short-lived self-signed delegation certs ("everyone is their own
  Azimuth"); revocation between expiries is best-effort by design.
  Raw `hash(pubkey)` addressing ruled out earlier (no rotation).
  Chain remains optional *outside* the auth path (smart-account
  social recovery for roots). ~~Design targets a future separate~~
  _(2026-09-03: superseded by decision `talos-config-5w1` — the
  protocol lives here; this repo becomes a monorepo around it.)_
  Design targets a future separate
  project; captured here because the exploration happened in-repo.
- 2026-08-16 — Messaging-primitive branch: **identity-addressed
  send ruled out as the primitive** (public id = floodable inbox,
  confused-deputy with wallets attached; publicness should be opt-in
  policy, not default topology). **CapTP-style live session refs
  ruled out as the model** (actors are intermittently-connected
  sovereigns; must verify offline) — kept as a within-session
  optimization. Landed on: UCAN/SPKI-shaped cert-chain capabilities;
  same cert primitive as the branch-1 key delegations. Full worked
  model in task 51 annotations.
- 2026-08-16 — Rendezvous branch: **global DHT ruled out for v0**
  (imports sybil/eclipse into the core lookup path; public who-is-
  where table; domains want membership-gated publish anyway — can
  join later as another lookup actor behind the same facet).
  **Parent-as-sole-registry ruled out** (orphans long-lived actors on
  parent death; kept as one channel). Landed on: piggybacked signed
  location records + lighthouse-as-plain-actor per domain.
- 2026-08-16 — Economics branch: **enforced allowances via parent-
  owned smart accounts (ERC-4337 session keys) ruled out** — children
  become puppets (can't earn, can't outlive parent), contradicting
  actor sovereignty; also chain lock-in. Landed on: sovereign wallets
  + tranche funding on the renewal beat + observability (public
  ledger watching); blast radius bounded economically (tranche ×
  detection lag), not cryptographically. User-driven reversal of the
  agent's recommendation — exposed a real inconsistency with the
  branch-1 sovereignty pin.

## Unattended Windows guest on KubeVirt (2026-08-11→14)

- 2026-08-12 — Tried scripting the "Press any key to boot from CD" EFI
  prompt with a VNC keypress (vncdotool, reinstall script v1). Ruled
  out: timing window, requires VNC reachability from the operator's
  machine, not derivable in-cluster. Landed on: repack the ISO with
  Microsoft's own `efisys_noprompt.bin` as the El Torito EFI entry —
  the prompt never exists, reinstall becomes a pure API act.
- 2026-08-13 — Repack v1 extracted the ISO with xorriso. Ruled out: the
  stock ISO is UDF-bridge and the ISO9660/Joliet view xorriso reads
  cannot represent the >4GB `install.wim` — extraction died partway.
  Landed on: 7z extracts the UDF layer; xorriso stays as the rebuilder.
- 2026-08-12 — containerDisk delivery of the ISO via the ImageVolume
  path ruled out: needs k8s ≥1.35 (kubevirt#17460); feature gate
  disabled, ISO delivered by CDI DataVolume import instead.
- 2026-08-11 — Block-mode system disk ruled out: CDI's importer runs
  non-root and cannot open the raw device on a Block-mode Longhorn
  volume. Filesystem mode; revisit only alongside the virtio switch.

## Pod resolution of mesh names (2026-07-31)

- Resolved by ADR-0010 (`hostAliases` pin the issuer name to the
  siwe-oidc Service ClusterIP). Kept as a pointer only: the 70-min SSO
  outage that motivated it was a pod dialing a *mesh* address from a
  10.244.x source — nebula routes 10.42.0.0/16, so it only worked
  while the pod ran on cp1. Rule: pods talk to Services; the mesh is
  for hosts and browsers.
