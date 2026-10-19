# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-10-19, pins from git — `d0o0`'s durable copy)

The remainder of `d0o0` (protocol ADR-0010/0011): the hub's pins now
have the durable copy ADR-0010 demanded, in git.

- `config-server/policy/pins.go` — `LoadPins` over
  `talos/pins/<scheme>-<address>.json` `{address, policy0,
  rotations:[[rotate-root cert…]…]}`, strict like the recipe (file
  name must match the address; a typo is an error, not a skipped
  identity); `EncodePin` is the inverse an operator uses after a
  rotation. `FoldRotations` moved here from `nodeagent` — hub and
  node run the **same** fold (re-verify every cert, copy-on-write).
- `hubseal.go` `reloadPins()` — rebuild the set from the checkout:
  trivial per `-admin-address`, overridden by its file, plus any
  further identity pinned by git; installed wholesale under `mu`;
  the unseal path reads through `pin(addr)`. Runs at construction
  **and on every served git tip** (`main.go` gitsync after-hook) —
  on fly the tree arrives after the hub is built, so construction
  alone would never see the file. A malformed file at construction
  is fatal; on reload it keeps the installed set.
- If a reload leaves the held `speak-as` issuer un-admitted by every
  pin (a rotation landed by commit), the hub **re-seals its identity**
  (`issuer.Seal()`, new) and logs it — a member's own pin would
  refuse that speak-as anyway.
- Shipped `talos/pins/eth-0xf568….json` (owner wallet, trivial, no
  rotations) and carried `talos/pins` into the nix sandbox so
  `TestLoadShippedPins` guards the real file. Tests: `TestPinsFromGit`
  (construction, bad reload, re-pin, re-seal), `policy/pins_test.go`.
- Gates green: `go test ./...`, `scripts/test-iroh.sh`,
  `nix build .#config-server-bin`. Commits `25b8a5f`, `3267876`;
  **deployed** the same day.

## Loose threads

- `d0o0` closed 2026-10-19: hub `3267876` deployed, unsealed, serving
  the tip with `talos/pins/`, no `git: pins:` error (the trivial file
  logs nothing — only a rotated or dead pin does).
- Rotation has never been exercised live: `EncodePin` exists but no
  CLI assembles the k guardian certs into the file (the ADR-0011
  `unsealIssuer`-returns-the-key thread `l3on` bites at the first
  real one).
- Carried from 2026-10-22: no real task has run through the task
  layer (`kmlg`); `bsj` (Longhorn backup) blocks real use; retries
  have no backoff; `tmm5`, `88uq`, ntfy first page.

## Suggested next steps

- ~~Deploy the hub and close `d0o0`~~ — done 2026-10-19 (hub
  `3267876`; ADR-0010/0011 Accepted).
- Run the first real task (`kmlg`); decide ADR-0043.
- `bsj`.
