# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-18 (fourth session) — **`e8d` built and deployed: the hub binds
its own iroh endpoint; hubkey = `EndpointId` for real.** Commits
`4230731`, `fe8570b`, `2767808`; image `registry.fly.io/marnyg-talos-config:2767808`
running, unsealed (hubkey `f855ca55…`). A stranger's `#bundle` from a
laptop was answered **over iroh through fly's edge in 164 ms** with a
hubkey-signed `unauthorized` — the first envelope the production Issuer
ever received from outside its process.

- **`protocol/actor.Multi`**: one identity on N wires (Accept fans in,
  Dial moves on only from `ErrUnreachable`). The Issuer serves Enroll
  (in-memory) and members (iroh) from one inbox. No ADR: transport
  plumbing, not authority.
- **`iroh-transport` `Options.AdvertiseRelay`**: home on the relay
  child at loopback, advertise the public URL; `TestAdvertiseRelay`
  proves the relay forwards by `EndpointId` across names.
- **`config-server`**: `--iroh-relay` (`IROH_RELAY_URL` in fly.toml);
  `hubiroh.go` behind build tag `iroh`, stub keeps `go test ./...`
  C-free. Hub publishes a relay-only `reach-me-at` (7 d, 6 h refresh)
  and serves it at `/.well-known/talos-hub/reach-me-at`.
  `TestHubBeatOverIroh` = member on real iroh runs `#renew`+`#bundle`
  through a local relay (nix runs it).
- **Build**: `config-server/nix` (cgo; static musl variant),
  `fly/image.nix` (nix2container), `fly/deploy.sh`
  (`HUB_BUILDER=mar@nixos` builds in the box's store, pushes from
  there), `hub-image.yml` as CI fallback. `Dockerfile` deleted;
  `fly.toml` has no `[build]`. **19 MB RSS** sealed in the image smoke.
- Docs: README "Deploying the hub", domain-model Issuer row +
  cold-cache, ADR-0024 amendment (endpoint as built).

## Loose threads

- **`e8d` still open** pending your closure; ADR-0024's remaining items
  are the name map and Provisioner-as-actor — promote to Accepted after
  those or rule them a follow-up ADR.
- **Cold cache is two GETs** (`speak-as` + `reach-me-at`, one hostname);
  ADR-0024's confirmation says "at most one `/.well-known` fetch" — the
  amendment reads it as one hostname; veto if you want one document.
- **Hub location TTL = `GrantTTL` (7 d)**, not ADR-0001's ≈ 1 h sketch.
  Reasoned (hub doesn't roam, beats are days apart); not modelled.
- **The Kit carries no hub location**: a member needs the relay URL
  out-of-band (its own home relay) and fetches `reach-me-at` once;
  `359.8.3`/`359.8.4` should decide whether `Kit.Location` is worth it.
- **`Dockerfile.siweoidc` is broken** since the `../protocol` replace
  (`go mod download` on a lone `config-server/`); now three replaces.
  Surfaced, not fixed.
- The remote nix builder path: the darwin daemon (root) cannot use my
  ssh key, so `--builders` fails; `--store ssh-ng://mar@nixos
  --eval-store auto` is what works (`fly/deploy.sh` does this).
- Carried: `tqr` (flip `/sealed` on identity — now reasonable, a real
  member beats at the hubkey next), `kql` (blocked on `359.8.3`), no
  graceful shutdown in `config-server`, `DefaultMailbox = 64` /
  renewal-beat fraction unbeaded, w1 down (`0q0`, `kso`), `5gz`.

## Suggested next steps

- **`359.8.3`** cp1 agent: bind NodeId on iroh homed at the hub's relay,
  fetch `/.well-known/talos-hub/{speak-as,reach-me-at}`, enroll (v2
  message → Kit), beat `#renew`+`#bundle`, consume
  `policy.AcceptTable(KindNode)`; then `kql` tears the scratch relay
  down. Build via `talos/extensions/p0agent/build.sh` — the static musl
  chain is warm on `mar@nixos`.
- **Name map half of `#bundle`** (`359.8.2.3`): the Issuer's location
  cache now fills from real members' piggybacked `reach-me-at`s.
- `tqr`: make `/sealed` 503 on a sealed identity once one member beats.
