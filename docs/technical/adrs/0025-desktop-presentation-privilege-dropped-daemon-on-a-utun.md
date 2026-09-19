# ADR-0025: Desktop presentation is a root-launched, privilege-dropped daemon on a utun with fake IPs and split DNS

- Status: Proposed
- Date: 2026-09-19
- Records: decisions `talos-config-4fm`, `talos-config-fgr` (supersedes `talos-config-8j3`); spike `talos-config-eda`

## Context and Problem Statement

Mesh v3 members are dialed by key; IP survives only as a device-local
fiction (ADR-0016). Phase 2 needs that fiction on the admin laptop so
`talosctl`, `kubectl` and a browser can reach `cp1.mesh.internal` by
name over the identity plane while nebula still serves the rest. The
bridge mode (`irohup -bridge`, one loopback listener per (member,
facet)) proved the plane; it does not scale to names, and it left the
`/etc/hosts` SAN workaround in place. Three questions had to be settled
together: what the presentation is, where the network-facing code
runs, and which zone it serves.

## Decision Drivers

- Invariant 2, actor-owned state: the member key is the credential;
  its theft is valid for the 30 d membership runway. Key-at-rest
  confidentiality on a laptop that runs arbitrary dev-shell
  dependencies matters.
- Least privilege for code that parses network input (QUIC/TLS
  handshake on a public UDP port, packets off a tun, remote certs).
- One fake-IP dialect across devices (mobile already chose
  `198.18/15`, P0.2).
- Phase 2's "each step reversible, one consumer at a time": nebula
  must keep serving the names v3 has not taken yet.
- Zero fleet-side change to take the first consumer (certSANs).
- Cheap to run: one launchd job, no IPC protocol, no ongoing root.

## Considered Options

### Presentation

- **A. Declarative `/etc/hosts` + per-member loopback ports.** No root,
  ~1 line. Cons: not names, one port dialect per host, no browser
  story. Kept as a 3-line fallback.
- **B. Loopback aliases (`127.x` per member, natural ports).** Ruled
  out: macOS binds only `127.0.0.1` (lo0 carries a host route), so each
  alias needs root anyway, and `127/8` is a second fake-IP dialect.
- **C. utun + `198.18/15` fake IPs + split DNS.** Symmetric with
  mobile. Chosen (`4fm`).

### Where the network code runs

- **Root daemon (tailscaled model).** QUIC, gvisor and cert
  verification as root; key file root-owned. Chosen at first (`8j3`)
  on the claim that start-as-root-then-drop is unsound in Go on
  Darwin.
- **Privilege separation (SCM_RIGHTS).** A root helper creates the
  utun and passes the fd; the agent runs as the user. ~150 lines, a
  second launchd job, an fd-handoff protocol, and the helper needs a
  live control channel once route churn is in scope — its "one-shot"
  story does not survive contact.
- **Root-launched, privilege-dropped single daemon.** The `8j3`
  premise was backwards: Linux keeps credentials per thread (hence Go's
  `AllThreadsSyscall`); XNU keeps them on the proc, so one `Setuid`
  drops every thread. Verified empirically (8 goroutines pinned to OS
  threads before the drop; all lost root). Dominates both other
  options on every axis `8j3` named. Chosen (`fgr`).

### Zone

- **Mint a v3 zone.** Clean, but a fleet-wide certSAN change and an
  apply per machine to buy a rename.
- **Inherit `mesh.internal`.** Every existing SAN keeps working; the
  hazard (macOS resolves longest-suffix, one `/etc/resolver` file per
  domain, so v3's resolver shadows nebula's for the whole zone) is
  closed by gating: answer only names in the plane's name map, forward
  the rest to nebula's DNS. Chosen (`eda`).

## Decision Outcome

Chosen: **C, as one binary launched as root that drops to a dedicated
service user after the utun is up, serving the inherited zone gated
on the name map.**

Shape (`config-server/fakeip`, `cmd/irohup -tun`, nixos
`modules/darwin/services/talos-mesh.nix`):

1. launchd starts `irohup -tun` as root. `privilegedSetup` — the one
   function that runs privileged, taking no network input — creates
   the utun, assigns `198.18.0.1`, routes `198.18.0.0/15` into it,
   makes the state dir the service user's, then
   `setgroups/setgid/setuid` to `_talosmesh`. It asserts euid ≠ 0 and
   that `Setuid(0)` now fails before anything touches the network.
2. Everything after runs as `_talosmesh`: gvisor stack over the tun
   (`TunLink`, wireguard-go `tun.Device` ↔ gvisor `channel`), the iroh
   endpoint, the beat, cert verification. The state dir (member key,
   certs, name map) is the service user's — unreadable by the login
   user, writable on the beat, no key/certs split.
3. DNS is static: nix-darwin declares `/etc/resolver/mesh.internal →
   198.18.0.2`; the resolver lives inside the tun and answers only
   names the agent's name map has a live entry for, with a stable fake
   IP from `198.18.1.1` up; other in-zone names forward to
   `-dns-upstream` or get NXDOMAIN. `<fake IP>:<natural port>`
   (`policy.FacetPort`) is one stream to (member, facet).
4. Enrollment stays a user-session act: `talos-mesh-enroll` runs
   `irohup -enroll-only` as the service user and opens the wallet
   challenge URL in the invoking user's browser. launchd keeps the
   daemon alive only while `kit.json` exists.
5. Route churn: configd may flush routes on a network transition and
   the dropped process cannot re-add; the daemon watches the route,
   exits non-zero, and launchd restarts it as root.

### Consequences

- The admin CLI reaches members by name with no `/etc/hosts` and no
  per-facet listeners: verified 2026-09-19, `talosctl -e
  cp1.mesh.internal … version` → cp1 v1.12.6 through mDNSResponder →
  utun → gvisor → iroh, daemon as `_talosmesh`.
- No network-parsing code runs as root; a compromise of the daemon
  yields the member identity (as any model where the daemon holds the
  key), not the laptop.
- The local control surface (status, re-enroll, rekey) is a new
  ambient-authority boundary in a system whose premise is that
  authority is carried by certs. Not built yet; when it is, it needs
  `LOCAL_PEERCRED` and a written allow-list — "it is a unix socket" is
  not an answer.
- "A privileged step added after the drop" is the one way this rots;
  the euid assertion turns it into a startup failure.
- Linux desktop needs its own privileged setup and a systemd unit;
  `fakeip` itself is portable. Mobile should adopt `fakeip` (`phz`).
- `talosctl -n` is resolved on the node's side, so the node selector
  must be something the node resolves for itself — P2.1's problem.

### Confirmation

- Right if P2.1 lands with `endpoints: [cp1.mesh.internal]` and no
  host-side workaround, and the daemon survives a month of laptop
  sleep/wake and network changes with at most launchd restarts (`7c3`
  observes the restart path).
- Invalidated if the control surface cannot be kept narrow (then the
  root/user split is buying less than it costs), or if a consumer
  turns up that is `CGO_ENABLED=0` and cannot see `/etc/resolver` —
  `/etc/hosts` remains the escape hatch for that one.
