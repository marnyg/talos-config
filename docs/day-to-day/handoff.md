# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-11-07/08): Syncthing + old-disk migration (`7lxt`), then the facet vocabulary out of the binaries (`fa84`, `4rqw`)

- **Syncthing is live over the mesh** (ADR-0046 Accepted): a `syncthing`
  gateway facet (port 22000) → `k8s/apps/syncthing` (static peer on
  nas1, GUI `syncthing.gw.mesh.internal` behind the admins gate + GUI
  password). Data on the new `longhorn-bulk-encrypted` class — 2 LUKS
  replicas on nas1's bays, **not** in the Tigris backup; the peers are
  the redundancy. Folders by temperature: `notes` (every device), `docs`
  and `media` (desktop + cluster; `docs` also on the Mac). Desktop copy
  on the reformatted pre-NVMe SSD at `/home/mar/sync` (nixos
  `modules.my.syncthing`, rewritten); Mac via home-manager
  `services.syncthing` (`profiles/mac.nix`). All folders `idle`.
- **The disks**: of ~450 GB, 186 GB was Audible `.aax` (Libation
  re-liberates; ~200/243 already in `media/books/audible`), 200 GB a
  Nextcloud export ≡ `archive`; the sync set is ~30 GB, so dedup became
  a selective copy. Git trees went to Forgejo org `archive/` (7 repos,
  private); `repos.zip` kept as a file. **Nothing on the old disks was
  deleted.** `/dev/sda` (old root) was the one wiped.
- **A new facet no longer rebuilds the fleet** (swarm, 2 waves):
  ADR-0047 — receivers advertise `facet@port`, presentations read the
  port off the advertisement (bare-name fallback for old daemons);
  ADR-0048 — `facets:` section in `talos/mesh-policy-v3.yaml` (nickel +
  Go validated, `check.sh` now really runs its mutants), receivers'
  accept table = their own forward set, gateway generic `-forward`.
  Hub `88e74c8` + gateway `7f92008` live; `m6xc` closed.

## Loose threads

- **Phone**: last Syncthing peer; its APK still carries the pre-0047
  tables, so it needs one more build (`ptgc` keystore caveat) before it
  can reach `syncthing.gw` — then pair it for `notes`.
- `irohup -forward` on a *device* still validates against
  `policy.Default` (4rqw's report): "new facet = data" holds for hub +
  gateway, not for a laptop serving a new device-kind facet. Devices
  reading the vocabulary off the plane would retire `Default`.
- Deploy ordering is now a rule, not a one-off: a recipe with a new
  `facets:` row or any `policy` parse change ships **hub image first,
  recipe second** (old hub refuses every `#bundle` on an unknown key —
  `KnownFields(true)`); gateway args and image pin land in one commit.
- Thread `oh3l`: desktop→cluster first load ran ~8 MB/s on Wi-Fi —
  verify the iroh path verdict (direct vs fly relay) before reading it.
- Glossary "Facet" in `domain-model.md` still says "closed per receiver
  kind … compiled"; ADR-0048's reading not yet synced (proposed below).
- Carried: `docs/README.md` spikes paragraph still says the root app
  recurses `k8s/apps/`; ADR-0001 (tasks) still Proposed; PR 10
  `ready → done`; `f0jl` first stop→start; `9io` old plaintext PVs.

## Suggested next steps

- Build + install the APK (one rebuild, carries 0047), pair the phone
  for `notes`; then wipe the four old disks and `1TB-old/staging`.
- Sync the glossary "Facet" + "Recipe" lines to ADR-0048; retire
  `policy.Default` on devices when a device wants a new facet.
- Back to the task layer (`focus.md`): PR 10 → `done`, one board per tag.
