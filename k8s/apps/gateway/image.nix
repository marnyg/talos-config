# The gateway pod's image (Mesh v3 P2.3, talos-config-359.9.3), built
# by nix like the hub's (fly/image.nix): config-server is cgo against
# libiroh_ffi, so a Dockerfile build would have to rebuild iroh-ffi.
# x86_64-linux only; from a laptop build on the linux box:
#
#   k8s/apps/gateway/build.sh        # HUB_BUILDER=mar@nixos: build, push ghcr.io/marnyg/gateway:<rev>
#
# What is in it:
#   /usr/local/bin/gateway         static musl, -tags iroh (cmd/gateway)
#   /etc/ssl/certs/…               a CA bundle: the hub is reached over
#                                  web PKI first (invariant 3's stated
#                                  exception) and a scratch rootfs has
#                                  none (notes, 0.1.0's lesson)
#   /var/lib/gateway               the state mount point
{ pkgs, lib, self, nix2container, configServer }:
let
  root = pkgs.runCommand "gateway-root" { } ''
    mkdir -p $out/usr/local/bin $out/var/lib/gateway $out/tmp
    ln -s ${configServer}/bin/gateway $out/usr/local/bin/gateway
  '';
in
nix2container.buildImage {
  name = "ghcr.io/marnyg/gateway";
  tag = self.shortRev or self.dirtyShortRev or "dev";
  copyToRoot = [ pkgs.cacert root ];
  config = {
    Entrypoint = [ "/usr/local/bin/gateway" ];
    Env = [
      "PATH=/usr/local/bin"
      "SSL_CERT_FILE=/etc/ssl/certs/ca-bundle.crt"
    ];
    User = "65534:65534";
    WorkingDir = "/var/lib/gateway";
  };
}
