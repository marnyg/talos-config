# The CI runner image (talos-config-0m50): forgejo-runner in *host*
# mode plus everything a job needs to build this repo's flake and push
# the result to Attic — nix, git, attic, a shell. No docker, no
# docker-in-docker: a `host` label runs each step as a process inside
# this container, which is the only executor that needs no privilege
# (deployment.yaml; namespace.yaml has the PSS reasoning).
#
# nix here is single-user as root: no daemon, no nixbld group
# (`build-users-group =`), `sandbox = false` (no user namespaces
# without CAP_SYS_ADMIN). The store is the container's writable
# overlay — nothing persists across pod restarts, by design: every
# run is warmed from the substituters in /etc/nix/nix.conf (the LAN
# mirror of cache.nixos.org and Attic itself, both by Service name),
# so a warm run substitutes the toolchains and builds only what the
# push changed. A PVC at /nix would shadow the image's own store, and
# the dance to re-register the image's closure into it buys little
# over a gigabit LAN mirror.
#
# The image's own closure (nix, git, bash, …) IS under /nix/store but
# unknown to nix's database until the entrypoint runs
# `nix-store --load-db < /nix-support/registration` — closureInfo's
# registration of exactly those paths. Without it nix would see them
# as invalid and rewrite glibc under its own feet the first time a
# build needed it.
#
# Entry: write Attic's client config from the sealed token (a file
# under HOME, never argv), register with Forgejo from the sealed
# pre-shared secret (`create-runner-file` — the runner's UUID is the
# secret's first 16 hex chars, so the same secret is the same runner
# on every start and Forgejo keeps one row, bootstrap.md step 1), then
# daemon on /etc/forgejo-runner/config.yml (config.yaml).
#
#   RUNNER_SECRET   40 hex chars, the one registered in Forgejo
#   RUNNER_NAME     the runner's name there; default the pod's hostname
#   FORGEJO_URL     where to poll; the in-cluster Service
#   ATTIC_ENDPOINT, ATTIC_TOKEN
#                   pods push at attic.nix-cache.svc with a token of
#                   their own (nix-cache/bootstrap.md "Tokens")
#
# x86_64-linux only; from a laptop:
#
#   k8s/apps/forgejo-runner/build.sh   # HUB_BUILDER=mar@nixos: build, push ghcr.io/marnyg/forgejo-runner:<rev>
{ pkgs, lib, self, nix2container }:
let
  entrypoint = pkgs.writeShellApplication {
    name = "runner-entrypoint";
    runtimeInputs = [ pkgs.forgejo-runner pkgs.nix pkgs.coreutils ];
    text = ''
      : "''${RUNNER_SECRET:?secret forgejo-runner missing?}"
      : "''${ATTIC_TOKEN:?secret attic-ci missing?}"
      export HOME=''${HOME:-/root}
      : "''${FORGEJO_URL:=http://forgejo.forgejo.svc:3000}"
      : "''${ATTIC_ENDPOINT:=http://attic.nix-cache.svc.cluster.local/}"
      : "''${RUNNER_NAME:=''${HOSTNAME:-runner}}"

      # The dirs a job writes, made here rather than shipped: copyToRoot
      # keeps the store's 555 modes, and with every capability dropped
      # (deployment.yaml) root is only the owner — no CAP_DAC_OVERRIDE
      # to write a read-only dir it owns. `/` is 755, so creating them
      # works. (nix2container `perms` refuses dirs shared across
      # copyToRoot entries, e.g. /etc.)
      mkdir -p "$HOME" /data /workspace
      mkdir -p /tmp && chmod 1777 /tmp

      # The image's closure into nix's db (see header). Idempotent.
      nix-store --load-db < /nix-support/registration

      umask 077
      mkdir -p "$HOME/.config/attic"
      cat > "$HOME/.config/attic/config.toml" <<EOF
      default-server = "ci"

      [servers.ci]
      endpoint = "$ATTIC_ENDPOINT"
      token = "$ATTIC_TOKEN"
      EOF
      umask 022

      cd /data
      forgejo-runner create-runner-file --instance "$FORGEJO_URL" \
        --secret "$RUNNER_SECRET" --name "$RUNNER_NAME"
      echo "runner: $RUNNER_NAME → $FORGEJO_URL; attic → $ATTIC_ENDPOINT"
      exec forgejo-runner daemon --config /etc/forgejo-runner/config.yml
    '';
  };
  # /bin: what `run:` steps and nix's own fetchers expect to find.
  env = pkgs.buildEnv {
    name = "forgejo-runner-env";
    pathsToLink = [ "/bin" ];
    paths = with pkgs; [
      bash
      coreutils
      findutils
      gnugrep
      gnused
      gawk
      diffutils
      gnutar
      gzip
      xz
      zstd
      git
      curl
      jq
      nix
      attic-client
      forgejo-runner
    ];
  };
  registration = pkgs.closureInfo { rootPaths = [ env entrypoint pkgs.cacert ]; };
  root = pkgs.runCommand "forgejo-runner-root" { } ''
    mkdir -p $out/usr/local/bin $out/etc/nix $out/nix-support
    ln -s ${entrypoint}/bin/runner-entrypoint $out/usr/local/bin/runner-entrypoint
    cp ${registration}/registration $out/nix-support/registration
    echo 'root:x:0:0:root:/root:/bin/bash' > $out/etc/passwd
    echo 'root:x:0:' > $out/etc/group
    # Substituters by Service name (pods cannot resolve the mesh
    # names; nix-cache/bootstrap.md §3): the LAN mirror of
    # cache.nixos.org (priority 40, forwarded from upstream), Attic's
    # `main` (41, so nixpkgs paths never cost it a round trip),
    # cache.nixos.org itself last — the mirror answers from nas1, and
    # nas1 away is a slower run, not a failed one (connect-timeout,
    # fallback). The Attic key is `attic cache info main`.
    # connect-timeout 10, not the laptops' 5: for curl the budget
    # includes name resolution, and a resolver that loses one of the
    # parallel A/AAAA replies costs exactly 5 s (seen under docker on
    # the box, 2026-10-17; kube-dns is fine, this is margin).
    cat > $out/etc/nix/nix.conf <<'EOF'
    experimental-features = nix-command flakes
    sandbox = false
    build-users-group =
    substituters = http://nixos-mirror.nix-cache.svc.cluster.local http://attic.nix-cache.svc.cluster.local/main https://cache.nixos.org
    trusted-public-keys = main:XkJGH65OA/NHub3CozVJL7mIt7uTfqLAVgNoQbUIcHw= cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
    connect-timeout = 10
    fallback = true
    max-jobs = auto
    cores = 0
    accept-flake-config = false
    warn-dirty = false
    EOF
  '';
in
nix2container.buildImage {
  name = "ghcr.io/marnyg/forgejo-runner";
  tag = self.shortRev or self.dirtyShortRev or "dev";
  copyToRoot = [ pkgs.cacert root env ];
  config = {
    Entrypoint = [ "/usr/local/bin/runner-entrypoint" ];
    Env = [
      "PATH=/usr/local/bin:/bin"
      "SSL_CERT_FILE=/etc/ssl/certs/ca-bundle.crt"
      "NIX_SSL_CERT_FILE=/etc/ssl/certs/ca-bundle.crt"
      "HOME=/root"
      "USER=root"
    ];
    User = "0:0";
  };
}
