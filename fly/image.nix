# The hub's fly image, built by nix (talos-config-e8d). It replaced the
# Dockerfile when config-server became cgo: fly's remote builder has no
# libiroh_ffi.a and rebuilding iroh-ffi from source per deploy would
# duplicate the pin (iroh-go/nix/sources.nix) and cost ~10 min a miss.
# One derivation, x86_64-linux only, built on a linux builder (CI; or
# `--store ssh-ng://<linux box>` from a laptop — fly/deploy.sh) and
# pushed to fly's registry with nix2container's skopeo:
#
#   nix build .#hub-image                               the image.json
#   nix run   .#hub-image.copyToRegistry -- --dest-creds x:$(fly auth token)
#   fly deploy --image registry.fly.io/marnyg-talos-config:<tag>
#
# What is in it (mirrors the old Dockerfile + .dockerignore):
#   /usr/local/bin/config-server   static musl, -tags iroh
#   /usr/local/bin/iroh-relay      static musl, the same core pin
#   /usr/local/bin/entrypoint.sh   fly/entrypoint.sh
#   /app/talos                     the tracked talos/ tree minus
#                                  talosconfig.age and extensions/;
#                                  .age ciphertext only — decrypted into
#                                  tmpfs at unseal (invariant 8). Untracked
#                                  plaintext (talosconfig, clusters/*/
#                                  secrets.yaml) is invisible to a flake.
#   /bin/sh, cp, mkdir             busybox, for the entrypoint
#   /tmp                           relay child config (relay.go)
{ pkgs, lib, self, nix2container, configServer, irohRelay }:
let
  talos = lib.fileset.toSource {
    root = ../talos;
    fileset = lib.fileset.difference ../talos (lib.fileset.unions [
      ../talos/talosconfig.age
      ../talos/extensions
    ]);
  };
  # /tmp: the relay supervisor stages the child's TOML there (relay.go);
  # a base image gave it for free, a from-scratch layer does not.
  root = pkgs.runCommand "hub-root" { } ''
    mkdir -p $out/usr/local/bin $out/app $out/tmp
    ln -s ${configServer}/bin/config-server $out/usr/local/bin/config-server
    ln -s ${irohRelay}/bin/iroh-relay $out/usr/local/bin/iroh-relay
    install -m 0755 ${./entrypoint.sh} $out/usr/local/bin/entrypoint.sh
    ln -s ${talos} $out/app/talos
  '';
in
nix2container.buildImage {
  name = "registry.fly.io/marnyg-talos-config";
  # The commit, so `fly releases` says what is running; -dirty from an
  # uncommitted tree.
  tag = self.shortRev or self.dirtyShortRev or "dev";
  copyToRoot = [ pkgs.pkgsStatic.busybox root ];
  config = {
    Entrypoint = [ "/usr/local/bin/entrypoint.sh" ];
    Env = [ "PATH=/usr/local/bin:/bin" ];
  };
}
