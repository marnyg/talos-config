# The actors image (0bc.4.5): one image, two binaries. A parent names
# it by digest in #spawn and the driver runs it as a child; the same
# image, `--entrypoint provisioner`, is the provisioner Deployment /
# the docker host's container. Built by nix like the hub's and the
# gateway's (fly/image.nix, k8s/apps/gateway/image.nix): the binaries
# are cgo against libiroh_ffi. x86_64-linux only; from a laptop:
#
#   actors/build.sh        # HUB_BUILDER=mar@nixos: build, push ghcr.io/marnyg/sap-actors:<rev>
#
# What is in it:
#   /usr/local/bin/child          static musl, -tags iroh (cmd/child)
#   /usr/local/bin/provisioner    static musl, -tags iroh (cmd/provisioner)
#   /etc/ssl/certs/…              a CA bundle (the relay is web-PKI HTTPS)
#   /var/lib/sap-provisioner      the provisioner's state mount point
#
# User 65534: the k8s driver submits Jobs under PSS `restricted`, which
# requires runAsNonRoot; the child needs nothing more. A provisioner
# on a docker host that needs the socket runs with `--user 0`.
{ pkgs, lib, self, nix2container, actors }:
let
  root = pkgs.runCommand "sap-actors-root" { } ''
    mkdir -p $out/usr/local/bin $out/var/lib/sap-provisioner $out/tmp
    ln -s ${actors}/bin/child $out/usr/local/bin/child
    ln -s ${actors}/bin/provisioner $out/usr/local/bin/provisioner
  '';
in
nix2container.buildImage {
  name = "ghcr.io/marnyg/sap-actors";
  tag = self.shortRev or self.dirtyShortRev or "dev";
  copyToRoot = [ pkgs.cacert root ];
  config = {
    Entrypoint = [ "/usr/local/bin/child" ];
    Env = [
      "PATH=/usr/local/bin"
      "SSL_CERT_FILE=/etc/ssl/certs/ca-bundle.crt"
    ];
    User = "65534:65534";
  };
}
