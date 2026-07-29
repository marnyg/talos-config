# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-07-29 (late) — **Mesh v2 phase 1: the hub side is done and
running.** Five commits, `c74a2fd..b09b286`:

- `nebderive` — wallet-derived mesh CA + machine/device/hub identities,
  byte-stable CA, golden-tested against stock `nebula-cert` 1.10.3.
- `nebstack` — vendored trim of nebula's `service` with `ListenUDP`. The
  real blocker was never UDP capability: upstream's stack already
  registers `udp.NewProtocol`, but `*stack.Stack` is unexported with no
  accessor, exactly why `wgstack` is vendored too.
- `nebconf` — hub lighthouse+relay config as a pure function of
  (master, subnet, port), validated by nebula's own `configTest`.
- `nebdns` + `nebtest` — `mesh.internal` served on the overlay,
  e2e-resolved over a real handshake. `dnsRespond` is shared with wgdns,
  not copied.
- `nebseal` — wired into the existing unseal (one master, two overlays),
  `--mesh-*` flags, mesh failure non-fatal and reported on `/sealed`.

Also: absorbed and deleted the legacy `docs/handover.md` (see
`technical/guides/{gotchas,deployment}.md`), and noted a bounded
exception to invariant 5 for the dual-overlay window.

## Loose threads

- **Nothing mints machine or device certs yet**, so `cp1.mesh.internal`
  resolves to an address no one answers on. This is the next gap.
- **ADR-0003 obligation not yet met**: when the mesh `/config` route
  lands, it must keep the derived-admin-IP gate. The firewall rule
  (`admins` group) is defense in depth, not a replacement.
- Two ADR candidates raised and not yet decided: the disk-encryption
  posture (pre-existing gap, prose now in `guides/deployment.md`) and
  the `nebstack` vendoring rationale.
- Exploration-log section "Mesh v2 — hub DNS mechanism" says to delete
  itself once phase 1 lands. It has landed; deletion awaits a yes.
- Revocation/expiry policy (uuid dc04e3e8) still open before enrolling
  shared-space devices. `nebderive` keeps it cheap: leaf validity is
  caller-supplied.
- Mobile-app DNS push still unverified (folds into kill criterion 4).

## Suggested next steps

- Node side of phase 1: factory schematic + nebula extension
  (`talosctl upgrade`, no wipe), then compose-time cert injection using
  `nebderive.MachineKey`/`MachineIP` and the `machines` group.
- Then `nebup` enrollment for devices, then the ≥1 week dogfood
  measuring direct-vs-relay and throughput (kill criteria 2–4 armed).
- Two practices earned their keep this session and should continue: run
  the binary (not just tests — it caught dead code `go vet` missed), and
  `nix build` before committing anything touching `go.mod`.
