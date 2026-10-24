# The tasks pod image (k8s/apps/tasks). Built by nix like the other
# in-house images (workouts/image.nix): one static binary on a scratch
# rootfs. x86_64-linux only; from a laptop:
#
#   tasks/build.sh      # HUB_BUILDER=mar@nixos: build, push ghcr.io/marnyg/tasks:<rev>
#
# What is in it:
#   /usr/local/bin/tasks      static musl (cmd/tasks)
#   /var/lib/tasks            the data mount point (tasks.db = the event log)
#   /usr/share/zoneinfo       Go's time.LoadLocation needs it for -tz
#
# User 65534 (PSS restricted); the PVC is fsGroup'd to match.
{ pkgs, lib, self, nix2container, tasks }:
let
  root = pkgs.runCommand "tasks-root" { } ''
    mkdir -p $out/usr/local/bin $out/var/lib/tasks $out/tmp
    ln -s ${tasks}/bin/tasks $out/usr/local/bin/tasks
  '';
in
nix2container.buildImage {
  name = "ghcr.io/marnyg/tasks";
  tag = self.shortRev or self.dirtyShortRev or "dev";
  copyToRoot = [ pkgs.tzdata root ];
  config = {
    Entrypoint = [ "/usr/local/bin/tasks" ];
    Cmd = [ "-data" "/var/lib/tasks" "-listen" ":8080" ];
    Env = [
      "PATH=/usr/local/bin"
      "ZONEINFO=/share/zoneinfo"
    ];
    User = "65534:65534";
    WorkingDir = "/var/lib/tasks";
    ExposedPorts = { "8080/tcp" = { }; };
  };
}
