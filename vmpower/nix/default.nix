# vmpower/: the win2k25 power switch (talos-config-f0jl). Pure Go,
# standard library only — no go.sum, so `vendorHash = null` and there is
# no dependency that can rot behind a stale cache.
#
#   nix build .#vmpower-bin     — host build: bin/vmpower
#   nix build .#vmpower-static  — pkgsStatic (musl) for the image. Linux only.
#   nix build .#vmpower-image   — the pod image (../image.nix)
{ pkgs, lib, self }:
let
  src = lib.fileset.toSource {
    root = ../.;
    fileset = lib.fileset.unions [
      ../go.mod
      (lib.fileset.fileFilter (f: f.hasExt "go") ../.)
    ];
  };
  mk = { pkgs' }: pkgs'.buildGo126Module {
    pname = "vmpower";
    version = "0.1.0";
    inherit src;
    vendorHash = null;
    env.CGO_ENABLED = 0;
    ldflags = [ "-s" "-w" ];
    meta.description = "start/stop the win2k25 KubeVirt guest from a browser";
  };
in
{
  bin = mk { pkgs' = pkgs; };
  static = mk { pkgs' = pkgs.pkgsStatic; };
}
