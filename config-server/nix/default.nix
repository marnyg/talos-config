# config-server, the fly hub. cgo since talos-config-e8d: the hub binds
# its own iroh endpoint (hubiroh.go, build tag `iroh`), so the binary
# links libiroh_ffi.a. Since the protocol's extraction (ADR-0052) that
# library, the relay and the Go modules come from github.com/marnyg/sap:
# the modules by tag through go.mod, the native pieces through the
# flake input `sap` (its packages are passed in here). Two builds:
#
#   nix build .#config-server-bin     — the host's ordinary build (glibc/
#                                        libSystem-dynamic); checkPhase =
#                                        go test ./... incl. the hub beat
#                                        over a local iroh-relay, -race
#   nix build .#config-server-static  — pkgsStatic (musl) + -extldflags
#                                        -static: the binary fly/image.nix
#                                        ships. Linux only.
#
# buildGo126Module, not buildGoModule: go.mod says go 1.26 (first
# pinned for the since-deleted nebula dependency; the code now uses
# 1.26 stdlib) while pkgs.go is still 1.25.x here. The Dockerfile that
# used to pin the same toolchain is gone (fly deploys the nix image).
{ pkgs, lib, self, sap }:
let
  # The source root is the repo and modRoot points here, because the real
  # talos/mesh-policy-v3.yaml + blocklist + pins/ ride along on purpose: the
  # policy tests run against the shipped files (policy/ asserts its
  # facet vocabulary against the Nickel contract, the hub test compiles
  # a bundle from them), so the sandbox must carry them — a fixture
  # copy would un-guard the file (019ce97).
  src = lib.fileset.toSource {
    root = ../..;
    fileset = lib.fileset.unions [
      ../.
      ../../talos/mesh-policy-v3.yaml
      ../../talos/mesh-blocklist-v3.txt
      ../../talos/pins
      ../../verification/nickel/mesh-policy-v3.ncl
    ];
  };

  # ffi: the libiroh_ffi.a to link (glibc or musl); relay: the iroh-relay
  # the tests run.
  mk = { pkgs', ffi, relay, static ? false }:
    pkgs'.buildGo126Module {
      pname = "config-server";
      version = "0.1.0";
      inherit src;
      modRoot = "config-server";
      # vendorHash caveats (canonical note):
      #  1. git add new packages BEFORE recomputing. Flakes only see
      #     tracked files, so an untracked directory is invisible to
      #     `go mod vendor` and its imports get silently left out of
      #     the vendor dir — the build then fails with "import lookup
      #     disabled by -mod=vendor" for a module go.mod requires.
      #  2. The vendor derivation is fixed-output: nix reuses any store
      #     path matching the hash, so a stale-but-matching vendor dir
      #     survives `go mod tidy`. Force a recompute by setting a
      #     bogus hash and reading nix's "got:" line.
      #  3. CI job `vendor-hash` in .github/workflows/verify.yml rebuilds
      #     the FOD on every push — but we push straight to main and a red
      #     Actions tab is silent (2026-09-20: 14 red pushes unnoticed).
      #     `.githooks/pre-push` runs the same two commands BEFORE the
      #     push when the range touches a vendored input; wire it with
      #     `git config core.hooksPath .githooks` (AGENTS.md).
      #     The sap modules are vendored by version now (no local
      #     replace since ADR-0052), so the hash moves with go.mod/go.sum
      #     — and a cached FOD output still hides the drift (CI run
      #     34754508013: one job green from cache, another rebuilt the
      #     FOD and mismatched). After touching go.mod, check with two
      #     commands, in this order:
      #       nix build .#config-server-bin.goModules --no-link
      #       nix build .#config-server-bin.goModules --rebuild --no-link
      #     `--rebuild` implies `--check`, which only compares against an
      #     already-realised path; run alone against a cold store it
      #     aborts ("outputs ... are not valid, so checking is not
      #     possible") rather than checking anything.
      vendorHash = "sha256-dbOo4cHYvxCf2yTX7mu+DLZqKzZfFQSzh3MA5D+LhuA=";
      tags = [ "iroh" ];
      env.CGO_ENABLED = 1;
      env.CGO_LDFLAGS = "-L${ffi}/lib";
      nativeBuildInputs = [ relay ];
      # Vendoring resolves the module graph; it never links or runs the
      # relay, so keep iroh-ffi / iroh-relay out of the FOD's inputDrvs.
      # Otherwise the `vendor-hash` CI job spends ~12min on a cold Rust
      # build to reach a 7s check (run 35471088669). CGO_ENABLED stays 1:
      # it gates which cgo-guarded files are considered, so flipping it
      # would move the vendor content. CGO_LDFLAGS does not.
      # Applied via overrideAttrs, so merge rather than assign: a plain
      # `nativeBuildInputs = []` would also drop nixpkgs' own go /
      # gitMinimal / cacert and the vendor step would lose its toolchain.
      overrideModAttrs = prev: {
        env = (prev.env or { }) // { CGO_LDFLAGS = ""; };
        nativeBuildInputs = lib.subtractLists [ relay ] (prev.nativeBuildInputs or [ ]);
      };
      # -race needs glibc/libSystem; the musl build runs the suite plain.
      ldflags = [ "-s" "-w" ] ++ lib.optionals static [ "-linkmode" "external" "-extldflags" "-static" ];
      checkFlags = [ "-count=1" ] ++ lib.optionals (!static) [ "-race" ];
      preCheck = ''
        export IROH_RELAY_BIN=${relay}/bin/iroh-relay
        export GOCACHE=$TMPDIR/gocache-check
      '';
      passthru = { iroh-ffi-static = ffi; iroh-relay = relay; };
      meta = {
        description = "talos-config hub: config server, sealed-hub unseal, iroh home relay, Issuer/Enroll actors on iroh";
        mainProgram = "config-server";
      };
    };

in
{
  bin = mk { pkgs' = pkgs; ffi = sap.iroh-ffi-static; relay = sap.iroh-relay; };
  # Fully static (musl) hub binary for the fly image. Linux only:
  # pkgsStatic on darwin still links libSystem dynamically.
} // lib.optionalAttrs pkgs.stdenv.isLinux {
  static = mk { pkgs' = pkgs.pkgsStatic; ffi = sap.iroh-ffi-static-musl; relay = sap.iroh-relay-static; static = true; };
  # The relay child the image ships beside it: sap's, same pin as the
  # binding, same musl toolchain.
  irohRelayStatic = sap.iroh-relay-static;
}
