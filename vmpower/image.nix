# The vmpower pod image (k8s/apps/vmpower). One static binary on a
# scratch rootfs, like workouts/image.nix. x86_64-linux only; from a
# laptop:
#
#   vmpower/build.sh      # HUB_BUILDER=mar@nixos: build, push ghcr.io/marnyg/vmpower:<rev>
#
# What is in it:
#   /usr/local/bin/vmpower   static musl (cmd-less single package)
#
# No CA bundle on purpose: the only TLS peer is the in-cluster API
# server, verified against the mounted ServiceAccount ca.crt — there is
# no web PKI in this binary's path.
#
# User 65534 (PSS restricted), matching the manifests.
{ pkgs, lib, self, nix2container, vmpower }:
let
  root = pkgs.runCommand "vmpower-root" { } ''
    mkdir -p $out/usr/local/bin $out/tmp
    ln -s ${vmpower}/bin/vmpower $out/usr/local/bin/vmpower
  '';
in
nix2container.buildImage {
  name = "ghcr.io/marnyg/vmpower";
  tag = self.shortRev or self.dirtyShortRev or "dev";
  copyToRoot = [ root ];
  config = {
    Entrypoint = [ "/usr/local/bin/vmpower" ];
    Env = [ "PATH=/usr/local/bin" ];
    User = "65534:65534";
    WorkingDir = "/tmp";
    ExposedPorts = { "8080/tcp" = { }; };
  };
}
