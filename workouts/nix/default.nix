# workouts/: the self-hosted workout tracker (epic talos-config-yz75).
# Pure Go (modernc sqlite), no cgo, no build tags — `go test ./...` in
# the module is the whole gate and runs here as checkPhase.
#
#   nix build .#workouts-bin      — host build + tests: bin/workouts
#   nix build .#workouts-static   — pkgsStatic (musl) for the image. Linux only.
#   nix build .#workouts-image    — the pod image (../image.nix)
{ pkgs, lib, self }:
let
  src = lib.fileset.toSource {
    root = ../.;
    fileset = lib.fileset.unions [
      ../go.mod
      ../go.sum
      (lib.fileset.fileFilter (f: f.hasExt "go") ../.)
      ../store/schema.sql
      ../web/templates
      ../web/static
      ../strongimport/testdata
    ];
  };
  mk = { pkgs', static ? false }:
    pkgs'.buildGo126Module {
      pname = "workouts";
      version = "0.1.0";
      inherit src;
      # vendorHash: see the canonical note on config-server/nix/default.nix
      # (git add first; the pre-push hook and CI's vendor-hash job cover
      # this module too).
      vendorHash = "sha256-7IC/p5GlD2EZkDXQzkaZ7E19S/ABKEBsg68vt8pykis=";
      env.CGO_ENABLED = 0;
      ldflags = [ "-s" "-w" ];
      checkFlags = [ "-count=1" ];
      meta.description = "self-hosted workout tracker: PWA + SQLite, Strong importer";
    };
in
{
  bin = mk { pkgs' = pkgs; };
  static = mk { pkgs' = pkgs.pkgsStatic; static = true; };
}
