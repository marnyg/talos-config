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

Workers' open questions, not yet ruled or filed:

- **Postage vs invariant 5**: adding `postage` to a `*` chain *widens*
  acceptance (∅ → anyone who pays). Model treats `*` without postage as
  malformed; the glossary/invariant should say so (q-nlink OQ1).
- **Absent `endpoints`** = ∅ today (plain intersection). Harmless for
  `invoke`, decisive once a `reach-me-at` chain is verified (OQ2).
- **Uniform verb**: fold requires every link `can == invoke`;
  `Attenuate` now rejects `child.Can != parent.Can`. Relax to "equals
  the root's" before `reach-me-at`/`relay` chains reuse `VerifyChain`.
- `cert.validateAud` (`decode.go:114`) rejects the literal `"*"` — a
  `reach-me-at` loc cannot cross the wire via `Decode` yet.
- `envelope.Verify` drops `verified` on chain reject (clock contract
  says the mark advances on reject); `actor` works around it.
- `clock.Mark` has no internal mutex — every consumer guards it.
- `actor.Send` is serialised per edge (order over window); a windowed
  HWM is the alternative. Double sig verify (transport goroutine +
  loop). Cheap rejects are *signed* replies — silent close for bad-sig?
- `#renew` aud is strict `old.Aud == inv.From` (hot-key holders with a
  speak-as in the proof are refused).
- Chain-length cap unenforced; `Postage` conflict surfaces as
  `ErrUnknownCaveat` (taint) — a distinct error would diagnose better.
- Rooting extension along the chain requires `Delegable`; a
  non-delegable last link's aud never enters `verified`.
- `protocol/doc.go` layout comment still omits `envelope/`, `actor/`.
- **Static musl link unverified** — read the first CI `static` job.

## Suggested next steps

- Rule or file the threads above (`bd create … -l pi,thread|debt`).
- Prune `exploration-log.md` §M2 (ADR-0001 landed) — asked, pending.
- Domain-model: §Messaging rule (3) predates ADR-0001's aud rules; add
  Transport/Mailbox to the glossary (proposed, pending).
