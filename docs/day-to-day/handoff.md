# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-18 — **`359.8.5` policy compiler: design pinned (grill-design),
no code.** The hub is unsealed again. Docs-only commit.

- Three pins from the bead's note settled: (1) grant `target` is the
  wildcard `"*"` — protocol **ADR-0004 drafted (Proposed)**, kind ≡
  facet vocabulary; (2) the iroh relay is **not a facet** (keyless
  child, hook sees a NodeId only) — relay rows leave the recipe, hub
  facets = `hub-http` + Issuer actor facets; (3) hub → node `apid`
  stays on nebula until Phase 4 (note on `359.11.2`).
- Shape pinned: two recipe files during the dual plane
  (`mesh-policy.yaml` v2 frozen, `mesh-policy-v3.yaml` = fixture
  promoted minus relay rows); new pure package `config-server/policy`
  (`Load`, `Facets`, `ALPN(facet)="talos-mesh/<facet>/v1"`,
  `AcceptTable(kind)`, `Compile(recipe, caller identity, now)`);
  `host:` rules compile at `#bundle` time from the caller's member
  cert. "(b) per-receiver accept tables" restated as shared
  vocabulary — the recipe has no forward addresses.
- Written: ADR-0017 inline amendment; glossary (Facet, Attenuation in
  both scopes); exploration-log section with five rule-outs; `4un`
  carries the round-trip law; `zeb` filed for the cert change.

## Loose threads

- **ADR-0004 details not discussed, decided in drafting** — veto if
  wrong: mixed `["*", ed:…]` target sets reject at decode; a *consent*
  with `target: "*"` fails rule 4 by construction.
- `5gz` (relay gate) has a **cold-cache trap**: gating on the beat
  cache locks remote members out after every deploy unless the hub's
  iroh endpoint is dialable without the relay (`e8d`) or the hook
  fails open while cold. Decide there.
- `mesh-policy-v3.ncl` still lists `relay` under `facets.hub` and the
  fixture still has relay rows — both change in step 2 of the build.
- `359.8.2.3` stays in_progress: `Issuer#bundle` is its remaining
  half and waits on `Compile`.
- Carried: `tqr` (`/sealed` 503 flips when the first member beats),
  `kql` blocked on `359.8.3`, no graceful shutdown in `config-server`,
  `DefaultMailbox = 64` / renewal-beat fraction unbeaded, w1 down
  (`0q0`, `kso`).

## Suggested next steps

- **`zeb`** first, model-first: `authorize.qnt` `ANY` target +
  `invWildcardTargetNeverWidens`, then `decode.go`/`intersectID` +
  rapid law. Promote ADR-0004 to Accepted when it lands.
- Then `359.8.5` steps 2–4: promote the fixture, update the Nickel
  contract, build `config-server/policy`, write the `4un` suite
  against the three-line reference interpreter.
- `e8d` in parallel when ready to touch the fly build.
