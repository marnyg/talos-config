# The protocol network's fly image (root ADR-0051, protocol ADR-0012):
# the lighthouse and the relay it runs behind its admission gate, and
# nothing else — no config-server, no talos tree, no unseal. Built by
# nix like the hub's (fly/image.nix): the lighthouse is cgo against
# libiroh_ffi. x86_64-linux only; driver: actors/net/deploy.sh.
#
# What is in it:
#   /usr/local/bin/lighthouse    static musl, -tags iroh (cmd/lighthouse)
#   /usr/local/bin/iroh-relay    static musl, the same core pin; run by
#                                the lighthouse as a child (-relay-bin)
#   /etc/ssl/certs/…             a CA bundle. Not for any outbound call:
#                                iroh-relay builds an HTTPS client at
#                                startup once it has an access hook and
#                                panics with no root at all.
#   /tmp                         the relay child's config (relaygate.Service)
#   /var/lib/sap-lighthouse      the key's mount point (a fly volume: the
#                                id IS the network bundle members hold)
#
# The entrypoint is the binary; its flags are fly.toml's [processes]
# line, so who is a member is read in git, not in an image.
#
# Root: the volume is mounted root-owned and the machine is this one
# process tree. (The k8s lighthouse runs the actors image as 65534.)
{ pkgs, lib, self, nix2container, actors, irohRelay }:
let
  root = pkgs.runCommand "sap-net-root" { } ''
    mkdir -p $out/usr/local/bin $out/var/lib/sap-lighthouse $out/tmp
    ln -s ${actors}/bin/lighthouse $out/usr/local/bin/lighthouse
    ln -s ${irohRelay}/bin/iroh-relay $out/usr/local/bin/iroh-relay
  '';
in
nix2container.buildImage {
  name = "registry.fly.io/marnyg-sap-net";
  # The commit, so `fly releases` says what is running; -dirty from an
  # uncommitted tree.
  tag = self.shortRev or self.dirtyShortRev or "dev";
  copyToRoot = [ pkgs.cacert root ];
  config = {
    Entrypoint = [ "/usr/local/bin/lighthouse" ];
    Env = [
      "PATH=/usr/local/bin"
      "SSL_CERT_FILE=/etc/ssl/certs/ca-bundle.crt"
    ];
  };
}
