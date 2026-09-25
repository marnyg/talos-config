# actors/: the sovereign-actor protocol's runnable side (0bc.4.5). The
# drivers are C-free; the two binaries bind iroh and sit behind build
# tag `iroh` (as config-server's do), so this is the only place they get
# compiled and tested. Same shape as config-server/nix/default.nix:
#
#   nix build .#actors-bin       — host build: bin/{child,provisioner};
#                                   checkPhase = go test ./... -tags iroh -race
#   nix build .#actors-static    — pkgsStatic (musl) + -extldflags -static:
#                                   what actors/image.nix ships. Linux only.
#   nix build .#actors-image     — the child/provisioner image (image.nix)
{ pkgs, lib, self }:
let
  # go.mod `replace`s ../protocol, ../iroh-transport and ../iroh-go, so
  # the source root is the repo and modRoot points here.
  src = lib.fileset.toSource {
    root = ../..;
    fileset = lib.fileset.unions [
      ../go.mod
      ../go.sum
      (lib.fileset.fileFilter (f: f.hasExt "go") ../.)
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
      pname = "sap-actors";
      version = "0.1.0";
      inherit src;
      modRoot = "actors";
      # vendorHash: see the canonical note on config-server/nix/default.nix
      # (git add first; FOD drift after touching a replaced tree; the
      # two-step --rebuild check). The pre-push hook and CI's vendor-hash
      # job cover this module too.
      vendorHash = "sha256-Rb2KgnNs4PVaVYkY+7ismdQqgzflKmWXJwH7oJqWi9Y=";
      tags = [ "iroh" ];
      env.CGO_ENABLED = 1;
      env.CGO_LDFLAGS = irohGo'.cgoLdflags;
      # Keep iroh-ffi out of the vendor FOD's inputs (config-server's note).
      overrideModAttrs = prev: {
        env = (prev.env or { }) // { CGO_LDFLAGS = ""; };
      };
      ldflags = [ "-s" "-w" ] ++ lib.optionals static [ "-linkmode" "external" "-extldflags" "-static" ];
      checkFlags = [ "-count=1" ] ++ lib.optionals (!static) [ "-race" ];
      preCheck = ''
        export GOCACHE=$TMPDIR/gocache-check
      '';
      passthru = { inherit (irohGo') iroh-ffi-static; };
      meta.description = "sovereign-actor provisioner and child binaries on iroh (k8s + docker drivers)";
    };

  irohGo = import ../../iroh-go/nix { inherit pkgs lib self; };
  irohGoStatic = import ../../iroh-go/nix { pkgs = pkgs.pkgsStatic; inherit lib self; };
in
{
  bin = mk { pkgs' = pkgs; irohGo' = irohGo; };
  static = mk { pkgs' = pkgs.pkgsStatic; irohGo' = irohGoStatic; static = true; };
}
