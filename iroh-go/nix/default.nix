# iroh Go binding pipeline (task talos-config-ow7, P0.4 condition).
#
#   iroh-ffi (Rust, uniffi scaffolding) --cargo--> libiroh_ffi.{a,so|dylib}
#         |                                              |
#   uniffi-bindgen-go --library libiroh_ffi ----------> iroh/iroh_ffi.go + iroh.h
#         |                                              |
#   fixup.sh (two known bindgen defects)                  |
#         v                                              v
#   ../iroh/ (checked in, verified identical by `generated-check`)
#         |
#   buildGoModule (CGO, static link of libiroh_ffi.a) -> cmd/smoke
#
# Everything is fixed-output vendored (fetchCargoVendor); no network in any
# build. `regen` is the only thing that writes into the worktree.
{ pkgs, lib, self }:
let
  sources = import ./sources.nix { inherit (pkgs) fetchFromGitHub; inherit lib; };
  inherit (pkgs) stdenv;

  # crates.io now 403s generic User-Agents (python-requests, curl) on
  # /api/v1/crates/*/download. The pinned nixpkgs (2026-01) fetchCargoVendor
  # has no UA and uses that endpoint; nixpkgs master fixed it (own UA +
  # static.crates.io CDN). Backport those two lines here rather than bump
  # the flake's nixpkgs (brief: no unrelated input upgrades). Fixed-output,
  # so the hashes are unaffected. Drop this once nixpkgs is bumped past
  # the upstream fix.
  fetchCargoVendor = pkgs.rustPlatform.fetchCargoVendor.override {
    writers = pkgs.writers // {
      writePython3Bin = name: attrs: content:
        pkgs.writers.writePython3Bin name attrs
          (if name != "fetch-cargo-vendor-util" then content else
          builtins.replaceStrings
            [
              "    session = requests.Session()\n"
              "https://crates.io/api/v1/crates/{pkg[\"name\"]}/{pkg[\"version\"]}/download"
            ]
            [
              "    session = requests.Session()\n    session.headers[\"User-Agent\"] = \"nixpkgs-fetchCargoVendor/2 (https://github.com/NixOS/nixpkgs)\"\n"
              "https://static.crates.io/crates/{pkg[\"name\"]}/{pkg[\"version\"]}/download"
            ]
            content);
    };
  };
  buildRustPackage = pkgs.rustPlatform.buildRustPackage.override { inherit fetchCargoVendor; };

  # Native lib. iroh-ffi has crate-type = ["staticlib", "cdylib"]; both land
  # in $out/lib via cargoInstallHook. The workspace also contains iroh-js
  # (napi) — we build only the ffi package.
  iroh-ffi = buildRustPackage {
    pname = "iroh-ffi";
    inherit (sources.iroh-ffi) version src cargoHash;
    cargoBuildFlags = [ "-p" "iroh-ffi" "--lib" ];
    # The Rust test-suite needs network-ish fixtures; the Go smoke test is
    # our gate.
    doCheck = false;
    # build.rs writes a pkg-config file next to the target dir; keep it.
    postInstall = ''
      mkdir -p $out/lib/pkgconfig
      find target -name iroh.pc -exec cp {} $out/lib/pkgconfig/ \;
    '' + lib.optionalString stdenv.isDarwin ''
      # cargo leaves the cdylib's install_name pointing at the build dir;
      # anything that links the dylib would fail at load time.
      install_name_tool -id $out/lib/libiroh_ffi.dylib $out/lib/libiroh_ffi.dylib
    '';
    meta.description = "iroh ${sources.iroh-ffi.version} uniffi scaffolding as a C-ABI library";
  };

  # Upstream's workspace lock carries uniffi 0.31.0 twice (crates.io for the
  # generator, git for the test fixtures) — fetchCargoVendor cannot vendor
  # two sources of one name+version. We build only `bindgen`, so the source
  # is trimmed to that member before vendoring: the workspace Cargo.toml is
  # replaced and the lock is upstream's pruned to the member (same
  # versions, 106 of 244 packages, zero git sources; both files live in
  # ../regen/). Reproduce the lock with: copy bindgen/ + upstream
  # Cargo.lock next to the trimmed Cargo.toml, `cargo tree --offline`.
  uniffi-bindgen-go-src = pkgs.runCommand "uniffi-bindgen-go-src-trimmed" { } ''
    cp -r ${sources.uniffi-bindgen-go.src} $out
    chmod -R u+w $out
    cp ${../regen/uniffi-bindgen-go.workspace.Cargo.toml} $out/Cargo.toml
    cp ${../regen/uniffi-bindgen-go.Cargo.lock} $out/Cargo.lock
  '';
  uniffi-bindgen-go = buildRustPackage {
    pname = "uniffi-bindgen-go";
    inherit (sources.uniffi-bindgen-go) version cargoHash;
    src = uniffi-bindgen-go-src;
    cargoBuildFlags = [ "-p" "uniffi-bindgen-go" ];
    doCheck = false;
    meta.description = "uniffi → Go bindings generator (targets uniffi ${sources.uniffi-bindgen-go.uniffi})";
  };

  # Relay server from the core repo at the same 1.1.0 tag. nixpkgs' iroh-relay
  # is 0.95.1 (pre-1.0), so we build our own.
  iroh-relay = buildRustPackage {
    pname = "iroh-relay";
    inherit (sources.iroh) version src cargoHash;
    cargoBuildFlags = [ "-p" "iroh-relay" "--bin" "iroh-relay" "--features" "server" ];
    doCheck = false;
    meta.description = "iroh relay server ${sources.iroh.version}";
  };

  # Static-only view of the lib. With both libiroh_ffi.a and .dylib/.so in
  # the same -L dir every linker prefers the shared one; pointing Go at this
  # dir is what makes the Go binary carry iroh inside it (no runtime
  # dependency on the store path).
  iroh-ffi-static = pkgs.runCommand "iroh-ffi-static-${sources.iroh-ffi.version}" { } ''
    mkdir -p $out/lib
    ln -s ${iroh-ffi}/lib/libiroh_ffi.a $out/lib/
  '';

  # The Go package's link.go carries -liroh_ffi and the per-OS system libs;
  # the build only has to supply -L. This is the value for CGO_LDFLAGS.
  cgoLdflags = "-L${iroh-ffi-static}/lib";

  # Generate the Go package from the built library, apply fixups. Output:
  # $out/iroh/{iroh_ffi.go,iroh.h}. Pure — no network, no cargo registry
  # (the regen/ dummy crate satisfies the bindgen's `cargo metadata` call).
  generated = pkgs.runCommand "iroh-go-generated"
    {
      nativeBuildInputs = [ uniffi-bindgen-go pkgs.cargo pkgs.go_1_26 pkgs.gnused pkgs.perl ];
    } ''
    export HOME=$TMPDIR CARGO_NET_OFFLINE=true GOFLAGS=-mod=mod GOCACHE=$TMPDIR/gocache GOPATH=$TMPDIR/gopath
    cp -r ${../regen} regen && chmod -R u+w regen && cd regen
    lib=""
    for cand in ${iroh-ffi}/lib/libiroh_ffi.so ${iroh-ffi}/lib/libiroh_ffi.dylib; do
      [ -e "$cand" ] && lib="$cand"
    done
    [ -n "$lib" ] || { echo "no libiroh_ffi shared object in ${iroh-ffi}/lib" >&2; exit 1; }
    uniffi-bindgen-go --library "$lib" --config uniffi.toml --out-dir $out --no-format
    bash ${../regen/fixup.sh} $out/iroh/iroh_ffi.go
    gofmt -w $out/iroh/iroh_ffi.go
  '';

  # The checked-in package must equal what the pipeline produces. This is
  # the drift gate: bump a pin without regenerating and `nix build
  # .#iroh-go` fails here.
  generated-check = pkgs.runCommand "iroh-go-generated-check" { } ''
    for f in iroh_ffi.go iroh.h; do
      if ! diff -u ${../iroh}/$f ${generated}/iroh/$f; then
        echo "iroh-go/iroh/$f differs from the regenerated binding — run: nix run .#iroh-go-regen" >&2
        exit 1
      fi
    done
    touch $out
  '';

  # `nix build .#iroh-go`: the native lib + the generated Go source, one
  # store path, drift-checked.
  iroh-go = pkgs.symlinkJoin {
    name = "iroh-go-${sources.iroh-ffi.version}";
    paths = [ iroh-ffi ];
    postBuild = ''
            mkdir -p $out/go
            cp -r ${../iroh} $out/go/iroh
            # force the drift check into this closure
            ln -s ${generated-check} $out/.generated-check
            cat > $out/VERSIONS <<EOF
      iroh-ffi ${sources.iroh-ffi.version} (${sources.iroh-ffi.rev}), core iroh from its lock (1.0.2), uniffi ${sources.iroh-ffi.uniffi}
      uniffi-bindgen-go ${sources.uniffi-bindgen-go.version} (uniffi ${sources.uniffi-bindgen-go.uniffi})
      iroh-relay ${sources.iroh.version}
      EOF
    '';
  };

  # Smoke binary + `go test` gate (the test runs the same code in-process
  # and also the relay path with the iroh-relay binary from this flake).
  smoke = pkgs.buildGo126Module {
    pname = "iroh-go-smoke";
    version = "0.1.0";
    src = lib.fileset.toSource {
      root = ../.;
      fileset = lib.fileset.unions [ ../go.mod ../iroh ../cmd ];
    };
    vendorHash = null; # stdlib only
    subPackages = [ "cmd/smoke" ];
    env.CGO_ENABLED = 1;
    env.CGO_LDFLAGS = cgoLdflags;
    nativeBuildInputs = [ iroh-relay ];
    # Static link of the Rust archive into the Go binary (still a normal
    # dynamic executable against libc/libSystem). Fully static is a
    # linux-musl build — see README.
    ldflags = [ "-s" "-w" ];
    doCheck = true;
    checkFlags = [ "-v" "-count=1" ];
    preCheck = ''
      export IROH_RELAY_BIN=${iroh-relay}/bin/iroh-relay
      export GOCACHE=$TMPDIR/gocache-check
    '';
    passthru = { inherit iroh-ffi iroh-relay; };
  };

  # nix run .#iroh-go-regen — regenerate ../iroh in the worktree.
  regen = pkgs.writeShellApplication {
    name = "iroh-go-regen";
    runtimeInputs = [ pkgs.git ];
    text = ''
      root="$(git rev-parse --show-toplevel)"
      dst="$root/iroh-go/iroh"
      mkdir -p "$dst"
      cp -f ${generated}/iroh/iroh_ffi.go ${generated}/iroh/iroh.h "$dst"/
      chmod u+w "$dst"/iroh_ffi.go "$dst"/iroh.h
      echo "regenerated $dst from iroh-ffi ${sources.iroh-ffi.version} with uniffi-bindgen-go ${sources.uniffi-bindgen-go.version}"
      git -C "$root" status --short iroh-go/iroh
    '';
  };
in
{
  inherit iroh-ffi iroh-ffi-static uniffi-bindgen-go iroh-relay generated iroh-go smoke regen cgoLdflags;
}
