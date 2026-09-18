# iroh-transport (task talos-config-0bc.2.6): the sovereign-actor protocol's
# actor.Endpoint over an iroh Endpoint. A library module, so the nix
# package is its test suite (the two-actor handshake over iroh, direct and
# via a local iroh-relay), built and linked exactly as iroh-go/nix builds
# cmd/smoke: cgo against the static-only libiroh_ffi.a view.
#
#   nix build .#iroh-transport         go test ./... (direct + relay handshake)
#   nix build .#iroh-transport-static  the same under pkgsStatic (musl on
#                                      linux) with -extldflags -static; the
#                                      Talos-extension feasibility probe.
#                                      See ../README.md for the recorded result.
{ pkgs, lib, self }:
let
  # The module `replace`s ../protocol and ../iroh-go, so the source root is
  # the repo and modRoot points at this directory. Only what `go mod
  # vendor` and the compiler need is included.
  src = lib.fileset.toSource {
    root = ../..;
    fileset = lib.fileset.unions [
      ../go.mod
      ../go.sum
      (lib.fileset.fileFilter (f: f.hasExt "go") ../.)
      ../../protocol/go.mod
      ../../protocol/go.sum
      (lib.fileset.fileFilter (f: f.hasExt "go") ../../protocol)
      ../../iroh-go/go.mod
      ../../iroh-go/iroh
    ];
  };

  # One test derivation per package set. pkgs' = pkgs gives the ordinary
  # (glibc/libSystem-dynamic) build; pkgs' = pkgs.pkgsStatic the musl one.
  # irohGo' must be the iroh-go pipeline evaluated for the SAME package set
  # so libiroh_ffi.a matches the C toolchain.
  mkTests = { pkgs', irohGo', static ? false }:
    pkgs'.buildGo126Module {
      # pkgsStatic's stdenv already suffixes the name (-static-<triple>).
      pname = "iroh-transport";
      version = "0.1.0";
      inherit src;
      modRoot = "iroh-transport";
      # protocol's third-party deps (secp256k1, jcs, x/crypto, rapid) plus
      # the two local replaces (../protocol, ../iroh-go) vendored from the
      # source tree. Caveats (git add first; FOD drift after touching
      # protocol/*.go or iroh-go/iroh/*.go; recompute recipe): see the
      # canonical vendorHash note on config-server-bin in flake.nix.
      vendorHash = "sha256-1bnXj0i00ryhxoYVFt3A3ZJKSTu3KuB+asRTHaUd5Jk=";
      env.CGO_ENABLED = 1;
      env.CGO_LDFLAGS = irohGo'.cgoLdflags;
      nativeBuildInputs = [ irohGo'.iroh-relay ];
      # -race needs glibc/libSystem; the musl build runs the suite plain.
      ldflags = [ "-s" "-w" ] ++ lib.optionals static [ "-linkmode" "external" "-extldflags" "-static" ];
      doCheck = true;
      checkFlags = [ "-v" "-count=1" ] ++ lib.optionals (!static) [ "-race" ];
      preCheck = ''
        export IROH_RELAY_BIN=${irohGo'.iroh-relay}/bin/iroh-relay
        export GOCACHE=$TMPDIR/gocache-check
      '';
      # A library has no binaries; leave a marker so the output is not empty
      # and records what was linked.
      postInstall = ''
        mkdir -p $out
        cat > $out/TESTED <<EOF
        iroh-transport tests passed against ${irohGo'.iroh-ffi-static}/lib/libiroh_ffi.a
        static=${lib.boolToString static} system=${pkgs'.stdenv.hostPlatform.system}
        EOF
      '';
      passthru = { inherit (irohGo') iroh-ffi iroh-ffi-static iroh-relay; };
      meta.description = "sovereign-actor Transport over iroh: two-actor handshake test over direct + relay paths";
    };

  irohGo = import ../../iroh-go/nix { inherit pkgs lib self; };
  irohGoStatic = import ../../iroh-go/nix { pkgs = pkgs.pkgsStatic; inherit lib self; };
in
{
  tests = mkTests { pkgs' = pkgs; irohGo' = irohGo; };
  # Feasibility probe for a fully static binary (Talos extension target,
  # iroh-go/README.md "CGO / linking story" row (b)). Only meaningful on
  # linux (musl); pkgsStatic on darwin still links libSystem dynamically.
  static = mkTests { pkgs' = pkgs.pkgsStatic; irohGo' = irohGoStatic; static = true; };
  # Fully static iroh-go/cmd/{smoke,p0relay,p0agent} (musl): the Mesh v3
  # P0.1 probe and the P0.3 Talos-extension agent, binaries that run in any
  # Linux container / on a Talos node. Tests off — `static` above is the
  # gate; this is a tool build.
  p0relayStatic = irohGoStatic.smoke.overrideAttrs (old: {
    pname = "p0relay-static";
    ldflags = old.ldflags ++ [ "-linkmode" "external" "-extldflags" "-static" ];
    doCheck = false;
  });
}
