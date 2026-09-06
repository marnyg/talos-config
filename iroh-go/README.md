# iroh-go — in-house Go binding for iroh

The Go binding the P0.4 API-churn probe made a condition of Mesh v3
(`docs/mesh-v3-iroh.md`, task `talos-config-ow7`): generated here, from
n0's own `iroh-ffi` crate, with `uniffi-bindgen-go`. No dependency on the
community `iroh-go`. Own `go.mod`; `protocol/` and `config-server/` do not
import this (the protocol is transport-independent).

```
iroh-ffi 1.1.0 (Rust, uniffi scaffolding) ──cargo──▶ libiroh_ffi.{a,dylib|so}
                                                          │
uniffi-bindgen-go 0.7.1 ──library mode──▶ iroh/iroh_ffi.go + iroh/iroh.h
                                                          │
regen/fixup.sh (3 textual fixes) ──▶ checked in, drift-checked by nix build
                                                          │
Go (cgo, static link of libiroh_ffi.a) ──▶ cmd/smoke, later hub / Talos ext / desktop
```

## Versions

| component | version | source | note |
|---|---|---|---|
| iroh-ffi | **1.1.0** | github `n0-computer/iroh-ffi` tag `v1.1.0` (`5e45109`) | `publish = false` — the git tag is the only distribution |
| core iroh / iroh-relay (linked into the lib) | **1.0.2** | iroh-ffi's `Cargo.lock`, taken as-is | ffi declares `iroh = "1.0.0"`; the lock pins 1.0.2, not 1.1.0. Bumping the lock to core 1.1.0 was tried: compiles, identical Go output, smoke passes (see report) — deferred to the orchestrator whether to carry that lock delta |
| uniffi (in iroh-ffi) | 0.31.2 | Cargo.lock | |
| uniffi-bindgen-go | **0.7.1+v0.31.0** | github `NordSecurity/uniffi-bindgen-go` (`0b7fb4c`) | targets uniffi 0.31.x — MUST stay on the same minor as iroh-ffi's uniffi; Go `init()` checks a contract version + per-function checksums and panics on mismatch |
| iroh-relay (server binary, tests only) | **1.1.0** | github `n0-computer/iroh` tag `v1.1.0` | nixpkgs ships 0.95.1 (pre-1.0), unusable |
| Rust toolchain | 1.93.0 | nixpkgs (flake input) | iroh-ffi MSRV 1.91, uniffi-bindgen-go MSRV 1.87 |
| Go | 1.26 | nixpkgs `buildGo126Module` | generated code needs ≥1.19 |

All pins live in `nix/sources.nix`.

## Layout

- `iroh/` — the Go package (`import "github.com/marnyg/talos-config/iroh-go/iroh"`).
  `iroh_ffi.go` and `iroh.h` are **generated**; `link.go` is hand-written
  (cgo link flags).
- `cmd/smoke/` — the acceptance test (`smoke.go` is also run by `go test`).
- `regen/` — everything the generator needs: `uniffi.toml` (Go config),
  `fixup.sh` (post-processing), a dependency-free dummy crate (the bindgen
  insists on `cargo metadata` in its cwd), copies of the upstream locks
  (`iroh-ffi.Cargo.lock` for transparency; `uniffi-bindgen-go.Cargo.lock`
  + `...workspace.Cargo.toml` are actually used — see nix/default.nix).
- `nix/` — the derivations. `flake.nix` exposes them as
  `packages.{iroh-go,iroh-go-smoke,iroh-ffi,iroh-relay,uniffi-bindgen-go}`
  and `apps.iroh-go-regen`.

## Build / test

```sh
nix build .#iroh-go          # lib/libiroh_ffi.{a,dylib|so} + go/iroh/, drift-checked
nix build .#iroh-go-smoke    # smoke binary; checkPhase runs the direct AND relay smoke
result/bin/smoke                              # direct, RelayMode::Disabled
result/bin/smoke -relay http://127.0.0.1:3340 # against a running iroh-relay --dev

# plain go, outside nix: point cgo at a dir containing libiroh_ffi.a
CGO_ENABLED=1 CGO_LDFLAGS="-L$(nix build .#iroh-ffi-static --print-out-paths)/lib" go test ./...
```

`go test ./...` runs `TestSmokeDirect` always and `TestSmokeRelay` when
`IROH_RELAY_BIN` is set or `iroh-relay` is on `PATH` (skips otherwise;
the nix build always runs both).

Note: with both `libiroh_ffi.a` and the shared lib in the same `-L` dir,
the linker picks the shared one (and, outside nix, the macOS dylib's
`install_name` is the cargo build dir). `nix/default.nix` therefore links
against a static-only view (`iroh-ffi-static`). Do the same by hand.

## Regenerating (version bump)

1. Edit `nix/sources.nix`: new `rev`, set the affected `hash`/`cargoHash`
   to `lib.fakeHash`, `nix build .#iroh-ffi` (and `.#uniffi-bindgen-go` /
   `.#iroh-relay` if bumped), paste the `got:` hashes.
   For uniffi-bindgen-go also refresh `regen/uniffi-bindgen-go.Cargo.lock`
   (copy `bindgen/` + upstream `Cargo.lock` next to the trimmed workspace
   `Cargo.toml`, run `cargo tree --offline`, take the pruned lock).
2. `nix run .#iroh-go-regen` — rebuilds the lib, runs the bindgen, applies
   `regen/fixup.sh`, `gofmt`, and copies `iroh_ffi.go`/`iroh.h` into
   `iroh/`.
3. `nix build .#iroh-go-smoke` (drift check + smoke) and fix whatever the
   compiler says in the consumers.
4. Commit `nix/sources.nix`, `regen/*`, `iroh/*` together.

If `fixup.sh` stops matching (generator changed shape), the build fails
loudly at `go vet`/compile — the fixes are exact-match `sed`/`perl`, no
silent partial application. Check the three defects below against the
new generator version and drop the ones that got fixed upstream.

Measured on the 1.0.2 → 1.1.0 core bump (ffi crate unchanged): `cargo
update` + rebuild ≈ 4 min wall, regenerated binding byte-identical,
smoke green; no Go changes. A bump that changes the ffi surface costs
that plus the compiler-guided fixes in the consumers.

## Known bindgen defects (fixed in `regen/fixup.sh`)

uniffi-bindgen-go v0.7.1 output for iroh-ffi needed three textual fixes;
nothing in the uniffi feature set iroh-ffi uses (async methods,
async constructors, callback/trait interfaces with foreign impls,
error objects, records, enums with data, `Display`/`Eq`/`Hash` traits)
needed a generator patch:

1. `HashMap<Vec<u8>, T>` → `map[[]byte]T` — invalid Go (slice keys).
   Only `EndpointOptions.Protocols` (ALPN → handler). Rendered as
   `map[string]T`, key converted at the FFI boundary.
2. `IrohError.Error()` returned the literal `"IrohError"` by value (vet
   `copylocks`; useless messages). Now `(*IrohError).Error()` → `Message()`.
3. `package_name` in `uniffi.toml` only names the output directory; the
   package clause is always the crate namespace (`iroh_ffi`). Renamed to
   `iroh`.

Ergonomic warts left as generated: `Option<Arc<T>>` becomes `**T`
(`Endpoint.AcceptNext() **Incoming`, `EndpointOptions.RelayMode **RelayMode`);
optional scalars are pointers (`*string`, `*[][]byte`); every object has a
`Destroy()` (finalizers are set, but call it on hot paths). Async Rust
methods block the calling goroutine — use goroutines for concurrency (as
`cmd/smoke` does).

## API shape (one snippet)

```go
preset := iroh.PresetMinimal()            // crypto provider only: no n0 DNS, no pkarr, no default relays
mode := iroh.RelayModeDisabled()          // or iroh.RelayModeCustomFromUrls([]string{"https://hub.example"})
bindAddr := "0.0.0.0:0"
alpns := [][]byte{[]byte("mesh/apid/v1")}
ep, err := iroh.EndpointBind(iroh.EndpointOptions{
	Preset: &preset, BindAddr: &bindAddr, Alpns: &alpns, RelayMode: &mode,
})
// dial by key (+ relay URL and/or direct addrs the caller already knows):
conn, err := ep.Connect(iroh.NewEndpointAddr(peerID, &relayURL, nil), []byte("mesh/apid/v1"))
bi, err := conn.OpenBi()
err = bi.Send().WriteAll(payload); err = bi.Send().Finish()
reply, err := bi.Recv().ReadToEnd(1 << 20)
```

## CGO / linking story per embedding

The binding is cgo: `CGO_ENABLED=1` and a C toolchain at build time,
always. The Rust archive is linked **statically** into the Go binary
(`-liroh_ffi` against `libiroh_ffi.a`); what remains dynamic is the
platform libc and, on macOS, system frameworks.

| target | status | how |
|---|---|---|
| **(a) fly container, linux/amd64 (hub)** | **unverified on x86_64** (no x86_64-linux builder here); the derivation evaluates for `x86_64-linux`; aarch64-linux verified in a container, see report | `nix build .#iroh-go-smoke` on an x86_64-linux builder: glibc-dynamic Go binary with iroh inside; drop into the existing `nix2container` image the same way as `config-server-bin`. Needs `pkgs.stdenv.cc` at build time only. Nothing else at runtime beyond glibc. |
| **(b) Talos system extension** | **unverified**; two options identified | Talos extensions run as containers on Talos' rootfs — a glibc-dynamic binary works if the extension image carries glibc (nix closure does that for free). For a fully static binary: build with `pkgsStatic`/musl — `pkgsStatic.rustPlatform` for iroh-ffi (`x86_64-unknown-linux-musl`, ring/aws-lc-rs compile fine for musl) + `CGO_ENABLED=1 pkgsStatic.buildGoModule` with `-ldflags '-extldflags -static'`. Expect to touch: `getrandom`/`libc` musl features (fine), `aws-lc-rs` (needs cmake + a C compiler; fine under nix), and iroh's netmon (netlink; no glibc dependence). Not attempted in this task. |
| **(c) desktop daemon (macOS today)** | **verified aarch64-darwin** | static archive + frameworks `Security SecurityFoundation SystemConfiguration CoreWLAN Foundation CoreFoundation` and `-lobjc -liconv` (all in `iroh/link.go`). 15.0 MB stripped binary, `otool -L` shows only system frameworks + libSystem (+ nix libiconv/libresolv when built in nix). Linux desktop = same as (a). |

Cross-compiling: Go alone cross-compiles trivially, cgo does not — every
target needs its own `libiroh_ffi.a` (Rust target) and a matching C
toolchain. Under nix that is `pkgsCross.<target>` for the whole chain
(one flake attribute per target); outside nix it is `cargo build
--target …` + `CC_<target>` + `CGO_LDFLAGS` by hand. Plan on **one CI
builder per (os, arch)** rather than cross-compiling from macOS.

## Sizes / times (aarch64-darwin, M-series, 2026-09-06)

- `libiroh_ffi.a` 21.8 MB, `libiroh_ffi.dylib` 15.1 MB (release, LTO —
  iroh-ffi's own profile).
- smoke binary: **15.0 MB** static-archive link, stripped; 3.8 MB when
  dynamically linking the dylib.
- iroh-relay binary 10.9 MB.
- nix cold: iroh-ffi ≈ 7–10 min; uniffi-bindgen-go ≈ 45 min (single huge
  askama crate — build once, cached); iroh-relay ≈ 8 min; bindgen run
  1.4 s; Go build of the binding + smoke ≈ 2 s. Warm (all cached): the Go
  step only.
