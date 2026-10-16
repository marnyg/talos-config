# The workouts pod image (k8s/apps/workouts). Built by nix like the
# other in-house images (fly/image.nix, actors/image.nix): one static
# binary on a scratch rootfs. x86_64-linux only; from a laptop:
#
#   workouts/build.sh      # HUB_BUILDER=mar@nixos: build, push ghcr.io/marnyg/workouts:<rev>
#
# What is in it:
#   /usr/local/bin/workouts      static musl (cmd/workouts)
#   /var/lib/workouts            the data mount point (workouts.db + media/)
#   /usr/share/zoneinfo          Go's time.LoadLocation needs it for -tz
#
# User 65534 (PSS restricted); the PVC is fsGroup'd to match.
{ pkgs, lib, self, nix2container, workouts }:
let
  root = pkgs.runCommand "workouts-root" { } ''
    mkdir -p $out/usr/local/bin $out/var/lib/workouts $out/tmp
    ln -s ${workouts}/bin/workouts $out/usr/local/bin/workouts
  '';
in
nix2container.buildImage {
  name = "ghcr.io/marnyg/workouts";
  tag = self.shortRev or self.dirtyShortRev or "dev";
  copyToRoot = [ pkgs.tzdata root ];
  config = {
    Entrypoint = [ "/usr/local/bin/workouts" ];
    Cmd = [ "serve" "-data" "/var/lib/workouts" "-listen" ":8080" ];
    Env = [
      "PATH=/usr/local/bin"
      "ZONEINFO=/share/zoneinfo"
    ];
    User = "65534:65534";
    WorkingDir = "/var/lib/workouts";
    ExposedPorts = { "8080/tcp" = { }; };
  };
}
