# Handoff — sovereign-actor protocol

<!-- "Where we left off" for the protocol scope. Overwritten per session.
     Deployment (talos/hub/fly) context lives in the root handoff. -->

## Last session

2026-09-12/13 — **M2 (`0bc.2`) built by a six-worker swarm; all merged.**
Worker reports (rulings, deviations, open questions) are in
`/tmp/swarm/{q-nlink,env-core,cert-verifychain,actor-runtime}.md` and
summarised in each bead's notes.

- `verification/quint/authorize.qnt` — `verifyChain` fold over
  `attenuate`, aud binding (signer | `*`+postage | principal via
  speak-as `verbs ∋ invoke` | `group:` sentinel), caveats v2; 18 laws,
  20 seeded mutants all killed. `quint verify` depth-2 now ~94 s.
- `protocol/cert` — `VerifyChain(receiver, consents, chain, speakAs,
  signer, facet, now) (eff, verified, err)`, `ErrGroupAud`; `Caveats`
  gains `Endpoints`, `Postage` (presence monotone, equal-or-taint);
  `Authorize` = `VerifyChain([g])` per grant + group rule; the 18 laws
  ported 1:1 (`authorize_chain_laws_test.go`); `VerifyBytes`.
- `protocol/envelope` — `Envelope`/`Reply`, JCS canonical, cost-ordered
  `Verify` (sig → target/seq → chain), per-edge `HWM` (atomic advance),
  strict `loc.iss == from`.
- `protocol/actor` — `Transport`/`Endpoint`/`Stream` ifaces, in-memory
  `MemoryNetwork`, serial bounded mailbox (`DefaultMailbox = 64`),
  `#renew` (same aud, same-or-narrower cav), location cache +
  piggyback; handshake test uses the real `VerifyChain`, 2- and 3-link.
- `iroh-transport/` (own module) — `actor.Endpoint` over iroh
  `PresetMinimal`, one ALPN `sovereign-actor/v1`, `ed:` ⇄ EndpointId
  byte identity, FIN framing, `iroh:udp=`/`iroh:relay=` tags; handshake
  over direct + local relay; nix package + Linux-only pkgsStatic probe.

## Loose threads

Workers' open questions are now beads (2026-09-13), all
`discovered-from` their source bead:

- bugs/debt: `ax7` `validateAud` rejects `"*"`; `kp4` `envelope.Verify`
  drops `verified` on reject; `02j` `clock.Mark` mutex; `3k5` double sig
  verify; `6tf` `ErrPostageConflict`; `djs` stale comments
  (`protocol/doc.go`, `check.sh` timing, `iroh-transport/doc.go`).
- rulings wanted (`thread`): `0lo` absent `endpoints` = ∅?; `xwu` verb
  uniformity before `reach-me-at`/`relay` chains; `5yj` per-edge serial
  `Send` vs windowed HWM; `7w5` signed cheap rejects; `7ei` strict
  `#renew` aud; `7n8` chain-length cap; `s8n` non-delegable last-link
  aud not observed by the mark.
- `cs3` — read the first CI `static` job, record the musl result.
- Postage-vs-invariant-5 ruled in the glossary (**Postage** entry):
  `*` without postage is malformed, not empty authority.

## Suggested next steps

- `cs3` first (cheap, gates the Talos-extension story), then `ax7`
  (blocks any `reach-me-at` on the wire) and `kp4`.
- Owner picks M3 (`0bc.3`) vs Phase 0 probes (`359.1.1–.3`).
