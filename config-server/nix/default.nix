# config-server, the fly hub. cgo since talos-config-e8d: the hub binds
# its own iroh endpoint (hubiroh.go, build tag `iroh`), so the binary
# links libiroh_ffi.a exactly as iroh-transport/nix does. Two builds:
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
{ pkgs, lib, self }:
let
  # go.mod `replace`s ../protocol, ../iroh-transport and ../iroh-go, so the
  # source root is the repo and modRoot points here. The real
  # talos/mesh-policy-v3.yaml + blocklist ride along on purpose: the
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
      ../../verification/nickel/mesh-policy-v3.ncl
      ../../protocol/go.mod
      ../../protocol/go.sum
      (lib.fileset.fileFilter (f: f.hasExt "go") ../../protocol)
      ../../iroh-transport/go.mod
      ../../iroh-transport/go.sum
      (lib.fileset.fileFilter (f: f.hasExt "go") ../../iroh-transport)
      ../../iroh-go/go.mod
      ../../iroh-go/iroh
    ];
  };

  mk = { pkgs', irohGo', static ? false }:
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
      #     Local `replace`s (../protocol, ../iroh-transport, ../iroh-go)
      #     are vendored from the source tree, so the hash changes
      #     whenever protocol/*.go, iroh-transport/*.go or iroh-go/iroh/*
      #     changes — and a cached FOD output hides the drift (CI run
      #     34754508013: one job green from cache, another rebuilt the
      #     FOD and mismatched). After touching a replaced tree, check
      #     with two commands, in this order:
      #       nix build .#config-server-bin.goModules --no-link
      #       nix build .#config-server-bin.goModules --rebuild --no-link
      #     `--rebuild` implies `--check`, which only compares against an
      #     already-realised path; run alone against a cold store it
      #     aborts ("outputs ... are not valid, so checking is not
      #     possible") rather than checking anything.
      vendorHash = "sha256-ZDsxvkW36LvujNR8/4Kqyv8jBIx8pA8X46CkrQKeA/I=";
      tags = [ "iroh" ];
      env.CGO_ENABLED = 1;
      env.CGO_LDFLAGS = irohGo'.cgoLdflags;
      nativeBuildInputs = [ irohGo'.iroh-relay ];
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
        nativeBuildInputs = lib.subtractLists [ irohGo'.iroh-relay ] (prev.nativeBuildInputs or [ ]);
      };
      # -race needs glibc/libSystem; the musl build runs the suite plain.
      ldflags = [ "-s" "-w" ] ++ lib.optionals static [ "-linkmode" "external" "-extldflags" "-static" ];
      checkFlags = [ "-count=1" ] ++ lib.optionals (!static) [ "-race" ];
      preCheck = ''
        export IROH_RELAY_BIN=${irohGo'.iroh-relay}/bin/iroh-relay
        export GOCACHE=$TMPDIR/gocache-check
      '';
      passthru = { inherit (irohGo') iroh-ffi-static iroh-relay; };
      meta = {
        description = "talos-config hub: config server, sealed-hub unseal, iroh home relay, Issuer/Enroll actors on iroh";
        mainProgram = "config-server";
      };
    };

  irohGo = import ../../iroh-go/nix { inherit pkgs lib self; };
  irohGoStatic = import ../../iroh-go/nix { pkgs = pkgs.pkgsStatic; inherit lib self; };
in
{
  bin = mk { pkgs' = pkgs; irohGo' = irohGo; };
  # Fully static (musl) hub binary for the fly image. Linux only:
  # pkgsStatic on darwin still links libSystem dynamically.
  static = mk { pkgs' = pkgs.pkgsStatic; irohGo' = irohGoStatic; static = true; };
  # The relay child the image ships beside it, same pin (iroh-go/nix/
  # sources.nix), same musl toolchain.
  irohRelayStatic = irohGoStatic.iroh-relay;
}
