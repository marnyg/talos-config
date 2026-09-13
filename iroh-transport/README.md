# iroh-transport

The sovereign-actor protocol's `actor.Endpoint` over an iroh Endpoint
(ADR-0001 §Transport & Facets; task talos-config-0bc.2.6). Its own Go
module so that `protocol/` never imports `iroh-go`:

```
protocol/actor  ◀── iroh-transport ──▶ iroh-go/iroh (cgo, libiroh_ffi.a)
```

Design (identity mapping, the single ALPN, FIN-delimited framing,
`iroh:udp=` / `iroh:relay=` endpoint tags, PresetMinimal with no
DNS/pkarr discovery) is in `doc.go`; the tests are the spec:

| test | what it pins |
|---|---|
| `TestTwoActorsHandshakeDirect` | the `protocol/actor` two-actor handshake (consent → grant → 3-link chain, real `VerifyChain`) over two iroh endpoints, direct UDP |
| `TestTwoActorsHandshakeRelay` | same, both endpoints homed on a local `iroh-relay --dev`, `iroh:relay=` tag round-trips through the published location record |
| `TestIrohTransportContract` | the `actor.Endpoint` contract: ids, `iroh:udp=` tags only with relay disabled, `ErrUnreachable` without an iroh hint / for `eth:` ids, FIN framing incl. empty reply on Close, pooled second dial, closed-endpoint errors |
| `TestConcurrentStreamsOnePeer` | many bi-streams share one pooled QUIC connection per peer |
| `TestActorIDEndpointIdRoundTrip`, `TestEndpointIDOfRejectsNonEd` | `ed:<hex>` ⇄ EndpointId is a byte identity; `eth:` ids are unreachable here |

## Build / test

By hand, exactly as `iroh-go` (README §Build / test):

```sh
cd iroh-transport
export CGO_ENABLED=1
export CGO_LDFLAGS="-L$(nix build ..#iroh-ffi-static --print-out-paths)/lib"
export IROH_RELAY_BIN="$(nix build ..#iroh-relay --print-out-paths)/bin/iroh-relay"
go test ./... -race -count=1
```

Without `CGO_LDFLAGS` the link fails with `library not found for
-liroh_ffi` — that is expected, the archive only exists in the nix store.
Without `IROH_RELAY_BIN` the relay test skips.

Under nix (what CI runs; `nix flake check` includes it):

```sh
nix build .#iroh-transport          # checkPhase = the suite above, -race
cat result/TESTED                   # which libiroh_ffi.a was linked
```

## Static link (Talos-extension probe)

`nix build .#iroh-transport-static` is the same suite built with
`pkgs.pkgsStatic` (musl) and `-linkmode external -extldflags -static`,
against an `iroh-ffi` built by the same `pkgsStatic` toolchain. It is
exposed on Linux only — on darwin `pkgsStatic` still links libSystem
dynamically, so a "static" darwin build proves nothing.

Status, 2026-09-13:

- **aarch64-darwin**: `nix build .#iroh-transport` green (6/6, relay
  handshake ~13 s). The static attribute is not exposed.
- **x86_64-linux**: first `static` job (run 34724490214, job
  103636292084, main @ ac91a93, 2026-09-12) **failed on nix plumbing,
  not on musl**: `iroh-ffi-1.1.0-vendor.drv` died with
  `ModuleNotFoundError: No module named 'requests'` from
  `fetch-cargo-vendor-util` — the crates.io UA override in
  `iroh-go/nix/default.nix` built the helper with `pkgsStatic.writers`,
  whose static python env lacks `requests`. No rust/cargo/cc ran, so
  the musl question is **still open**. Fix (this commit): the vendor
  fetcher, its writers, and the `iroh-ffi-src` prep now come from
  `pkgs.buildPackages`; the fixed-output vendor drv is one and the same
  for the glibc and musl builds
  (`nix eval .#packages.x86_64-linux.iroh-transport{,-static}.iroh-ffi.cargoDeps.drvPath`
  agree). **Read the next `static` job after this lands** and record
  it here. Expected friction, per iroh-go/README.md row (b):
  `aws-lc-rs` (cmake + C compiler under musl), `getrandom`/`libc` musl
  features, and whether `pkgsStatic.rustPlatform` picks the
  `x86_64-unknown-linux-musl` target for the FFI crate without extra
  `CARGO_BUILD_TARGET` plumbing.

If the probe fails for a musl-specific reason, the fallback for the
Talos extension is unchanged: a glibc-dynamic binary inside an extension
image that carries the nix closure (row (a)/(b) in iroh-go/README.md).
