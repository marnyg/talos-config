# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-11-21): any-port TCP on devices (`vjky`, ADR-0050 Proposed)

- **A facet may take any port.** A device started with
  `-forward 'tcp=<host>:*'` advertises `tcp@*`; another device reads
  `<name>.mesh.internal:<any port>` as (member, `tcp`, port) and names
  the port in a two-byte header at the head of each stream
  (`nodeagent.Route`/`WritePort`, `meshtun.Pool.OpenRoute`; receiver
  `Agent.spliceAny`). A facet advertised at exactly that port still
  wins (:22 stays `ssh`). No protocol, hub or gateway change.
- **Recipe**: `{name: tcp, kind: device, port: 1}` (nominal port) and
  `{facet: tcp, group: admins}`. The hub compiles the grant on its next
  poll; `admins` devices carry 8 grants instead of 7.
- **Not live**: no device serves it. It needs irohup rebuilt from this
  revision on both ends (nixos flake input; the Mac; the phone APK to
  call) and the flag on the serving end.
- Gates run: `go test ./...`, `scripts/test-iroh.sh config-server`
  (new section in `TestNodeAgentEndToEnd`), Nickel `check.sh`.

## Loose threads

- **ADR-0050 is Proposed, not reviewed**: facet name `tcp`, the `@*`
  advertisement (second in-band convention on `cav.facet` — ADR-0047's
  trigger for its option C), nominal `port: 1`, raw :443 on a name with
  no :80 facet. The domain model's "Facet" / zone-rule text still says
  ports live only in a facet definition; edits proposed, not written.
- **`127.0.0.1` as the any-port host exposes loopback-only services**
  to every `admins` device, with no second gate. Pick the host
  deliberately when enabling it.
- Carried from 2026-11-17/18 (`e6rx`): desktop H3 numbers taken on a
  busy desktop; w1's GPU capped at ~50 W; awaiting the owner's delete
  of the `longhorn-local` claims, win2k25 and `/var/lib/e6rx-scratch`;
  desktop tier-2 fetch may be stopped; H3 ref-to-video / image edit
  unmeasured.
- Carried from 2026-11-08: phone APK rebuild + Syncthing pairing; wipe
  the four old disks; glossary "Facet"/"Recipe" vs ADR-0048;
  `policy.Default` on devices; `oh3l`; ADR-0001 (tasks) Proposed; PR 10
  `ready → done`; `f0jl` first stop→start; `9io` old plaintext PVs.

## Suggested next steps

- Owner: read ADR-0050, accept or amend; then bump the nixos input,
  add the flag on one device and `curl <name>.mesh.internal:<port>`
  from another — the live confirmation the ADR still owes.
- With the next hub image: make a vocabulary row's port optional so
  `tcp` stops carrying a nominal one.
- Back to the task layer (`focus.md`).
