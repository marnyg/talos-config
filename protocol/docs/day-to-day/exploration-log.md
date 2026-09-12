# Exploration Log — sovereign-actor protocol

<!-- "What we've tried and ruled out." Prevents re-attempting dead ends across sessions.
     Granularity: strategy-level pivots only. Protocol scope only; the
     talos deployment's log is ../../../docs/day-to-day/exploration-log.md. -->

## M2 envelope + actor runtime (`0bc.2`, grill-design 2026-09-12)

- 2026-09-12 — **Separate `envelope.Verify` beside `cert.Authorize`**
  ruled out. Two verifiers with slightly different chain semantics
  (fixed 2-link `[consent, grant]` for connections vs N-link
  attenuated `proof` for envelopes) is exactly how a second authority
  mechanism sneaks in beside the one primitive (invariant 1), and
  `authorize.qnt` would model only one of them; `cert.Attenuate`
  would stay an orphan and speak-as resolution would be duplicated.
  Landed on: one chain verifier that folds `Attenuate` over N links
  with the sketch's four rules (first link R-signed; last `aud` =
  presenting signer or `*`; nothing expired; no unknown caveat); the
  connection-level `Authorize` becomes ALPN→facet + that verifier on
  `[consent, grant]` + the talos-only parts (member identity, groups,
  blocklist). Ordering consequence: `authorize.qnt` gains the N-link
  chain first, then the Go port — a prerequisite task, not part of M2.
- 2026-09-12 — **One ALPN per actor facet** ruled out: reopens the
  metadata leak `docs/mesh-v3-iroh.md` closed (fine-grained facet
  names in the ClientHello) and makes every new facet a new ALPN
  string. Landed on: stream facet = ALPN class at connect; actor facet
  = `to.facet` inside the encrypted envelope, one fixed ALPN class for
  all actor traffic; the verifier takes `facet` and is derivation-blind
  (glossary: Facet).
- 2026-09-12 — **Grants always issued to the hot key** (no aud-side
  speak-as resolution) ruled out: every correspondent of a root S
  would have to re-issue on each of S's hot-key rotations, defeating
  the cold/hot split. Landed on: last-link `aud` ∈ {signer key, `*`
  (fail closed unless a postage caveat is present), or principal S
  with a speak-as `S→signer` in the proof whose `cav.verbs ∋ invoke`};
  `group:` audiences stay connection-level (talos layer) in M2 — no
  member-cert slot in the envelope path and no actor use case yet.
- 2026-09-12 — **Nebula as M2 transport** ruled out: it hands out IPs,
  so a QUIC+ALPN stack would be needed on top anyway, and it is the
  thing Mesh v3 replaces. **Real cp1/w1 as M2's acceptance gate**
  ruled out: proves nothing about the protocol the in-process test
  does not, and couples M2 to `359.1.x` (fly scratch, Android, Talos
  extension). Landed on: `protocol/` gets a minimal `Transport`
  interface + in-memory impl; the iroh adapter is its own module
  beside `iroh-go/` mapping NodeId ↔ `ed:` ActorID (peer key = actor
  id); acceptance = two actors in one Go test over both transports.
  Owner's standing constraint: the end goal is dedicated Talos nodes,
  so the adapter must stay static-linkable (`pkgsStatic`, unattempted)
  and the deployment follow-up is a real task, not a nicety.
- 2026-09-12 — **Sig-less envelopes riding iroh's peer authentication**
  ruled out: makes correctness depend on an authenticating transport
  (couples the protocol to the transport, and the in-memory test
  transport would have to fake peer auth). Landed on: the envelope
  keeps its own `sig` (JCS canonical form, scheme-selected signature
  as for certs; `payload` opaque bytes, hashed not canonicalized),
  verified first in cost order; the transport peer key is a hint,
  never an authority input — signer == peer is NOT required on the
  envelope path (only the talos stream-facet path binds to `Peer`,
  because there is no envelope there).
- 2026-09-12 — **Nonce + timestamp window for replay** ruled out: puts
  the receiver's clock in the replay path and needs a nonce set per
  window; per-(sender, receiver) `seq` needs one int64 per
  correspondent and invariant 8 already blesses the volatile counter.
  **Proof chain on replies** ruled out: inverts the capability
  direction (every caller would hold a grant from every callee); the
  open bi-stream is the invitation. **Correlation ids / pending
  tables** ruled out: one invocation = one bi-stream gives correlation
  for free on both transports. (glossary: Envelope, Invocation, Reply)
- 2026-09-12 — **Concurrent `net/http`-style handlers** ruled out for
  the actor runtime: every handler author would reinvent locking over
  the HWM / consent / `clock.Mark` state and "actor" becomes
  decoration. Landed on: serial mailbox — transport goroutines do only
  the pure cheap rejections (decode, `sig`, `to.target == me`) and
  enqueue into a bounded mailbox; one actor goroutine does `seq`,
  chain verification, `Mark.ObserveAll`, handler, reply; full mailbox
  drops (invariant 12). Owner's framing: the actor is the primary
  shape, the stream facet is backwards compatibility so the same node
  can act as a plain VPN in front of e.g. Jellyfin (glossary: Facet).
  Caller-carried consents ruled out: the receiver prepends its own
  consent(s) to root the caller's links, as `Authorize` does today.
- 2026-09-12 — **"The cert being renewed is its own proof" for
  `#renew`** ruled out: a verifier special case ("any cert I signed
  authorizes my #renew") is a second authority path (invariant 1) and
  would let a `jellyfin` stream grant open an actor-facet conversation.
  Landed on: `#renew` is an ordinary facet behind an ordinary grant in
  the starter kit; declining a relationship = not issuing it. Reply
  gains `loc?` so renewal responses carry the grantor's location as
  the sketch requires (glossary: Renewal beat, Reply).
- 2026-09-12 — **n0's DNS/pkarr discovery** for reachability ruled
  out: a discovery authority we do not run (invariants 3, 11);
  `PresetMinimal` is the point of the Phase 0 probes. **A separate
  signed location-record type** ruled out (invariant 1). **"Deliver
  the envelope but discard a bad `loc`"** ruled out: one fail-closed
  rule. Landed on: `reach-me-at` Cert with `cav.endpoints` of
  transport-tagged opaque strings, `aud: *`, ~1 h; piggyback is the
  discovery layer, lighthouse (M3) the fallback (glossary: Location
  record).
