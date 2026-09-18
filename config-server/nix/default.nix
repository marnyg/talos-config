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
# buildGo126Module, not buildGoModule: the embedded nebula (slackhq/
# nebula 1.11.0, for the mesh CA + lighthouse/relay) requires go >=
# 1.26.0 while pkgs.go is still 1.25.x here. The Dockerfile that used to
# pin the same toolchain is gone (fly deploys the nix image).
{ pkgs, lib, self }:
let
  # go.mod `replace`s ../protocol, ../iroh-transport and ../iroh-go, so the
  # source root is the repo and modRoot points here. The real
  # talos/mesh-policy*.yaml + blocklist ride along on purpose: the policy
  # tests run against the shipped files (mesh/ reads v2, policy/ reads v3
  # and asserts its facet vocabulary against the Nickel contract, the hub
  # test compiles a bundle from them), so the sandbox must carry them — a
  # fixture copy would un-guard the file (019ce97).
  src = lib.fileset.toSource {
    root = ../..;
    fileset = lib.fileset.unions [
      ../.
      ../../talos/mesh-policy.yaml
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
      #  3. (CI job `vendor-hash` in .github/workflows/verify.yml rebuilds
      #     the FOD on every push, so drift fails there first.)
      #     Local `replace`s (../protocol, ../iroh-transport, ../iroh-go)
      #     are vendored from the source tree, so the hash changes
      #     whenever protocol/*.go, iroh-transport/*.go or iroh-go/iroh/*
      #     changes — and a cached FOD output hides the drift (CI run
      #     34754508013: one job green from cache, another rebuilt the
      #     FOD and mismatched). After touching a replaced tree, check
      #     with `nix build .#config-server-bin.goModules --rebuild`.
      vendorHash = "sha256-uv8UxS64y/+Qjlqbtywe64Cr7kn18/PiO9SzuM73nS8=";
      tags = [ "iroh" ];
      env.CGO_ENABLED = 1;
      env.CGO_LDFLAGS = irohGo'.cgoLdflags;
      nativeBuildInputs = [ irohGo'.iroh-relay ];
      # -race needs glibc/libSystem; the musl build runs the suite plain.
      ldflags = [ "-s" "-w" ] ++ lib.optionals static [ "-linkmode" "external" "-extldflags" "-static" ];
      checkFlags = [ "-count=1" ] ++ lib.optionals (!static) [ "-race" ];
      preCheck = ''
        export IROH_RELAY_BIN=${irohGo'.iroh-relay}/bin/iroh-relay
        export GOCACHE=$TMPDIR/gocache-check
      '';
      passthru = { inherit (irohGo') iroh-ffi-static iroh-relay; };
      meta = {
        description = "talos-config hub: config server, sealed-hub unseal, nebula lighthouse, Issuer/Enroll actors on iroh";
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
