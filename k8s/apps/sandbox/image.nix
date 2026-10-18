# The agent harness image (talos-config-tj7c, ADR-0035's "null option"):
# pi — the coding agent the owner already drives — plus the tools it
# shells out to, as one non-root image a Job runs under RuntimeClass
# kata — and, since e6pu, the protocol child beat (actors/cmd/child)
# beside it, so the same image is also what a parent names in #spawn.
#
# Three modes, chosen by the entrypoint from the environment:
#
#   SAP_INTRO set       child: the pod is a lease the sap-provisioner's
#                       driver started (driver.ParamsEnv). The entrypoint
#                       execs /usr/local/bin/child, which knocks on the
#                       parent's #birth, beats, and serves #task/#steer/
#                       #abort by running `pi --mode rpc` in /workspace
#                       and streaming its records up the parent's
#                       agent-events facet (actors/child). A #task that
#                       names a repo is cloned under /workspace and the
#                       result pushed as a branch + PR by the child
#                       itself (e6pu.3): FORGEJO_URL/TOKEN arrive by the
#                       driver's outfit like OPENROUTER_API_KEY (ADR-0037,
#                       never the intro) and are scrubbed before pi
#                       starts. AGENT_MODEL becomes SAP_PI_ARGS. Nothing
#                       below this line applies: no tmux.
#   AGENT_PROMPT set    batch: `pi -p` runs the prompt in /workspace and
#                       exits; the transcript is the Job's log.
#   AGENT_PROMPT unset  interactive: pi runs inside a detached tmux
#                       session named `agent`; the pod waits for it to
#                       end. Attach from a laptop whose kubeconfig
#                       reaches the API over the mesh:
#                         kubectl exec -it -n ai <pod> -- tmux attach
#                       (TMUX_TMPDIR=/workspace/.tmux is image env, so
#                       a bare `tmux attach` under exec finds the socket.)
#                       Detach with C-b d; the session (and the Job)
#                       lives on until pi exits or the deadline fires.
#
#   AGENT_MODEL         pi model id under the openrouter provider;
#                       default anthropic/claude-sonnet-4.6
#   OPENROUTER_API_KEY  from the sealed secret (agent.yaml); pi reads it
#
# The batch and interactive modes have no git: the workspace is a
# throwaway emptyDir. The shell clone-and-PR path that lived here from
# ivof.2 to e6pu.3 is gone — one implementation, in the child.
#
# `pi` is pkgs.pi-coding-agent from its own nixpkgs pin (flake input
# `nixpkgs-pi`, nixos-unstable): the repo's nixpkgs predates the
# package and bumping it drags every Go and Rust build along;
# `nix flake update nixpkgs-pi` bumps pi alone.
#
# Root filesystem is read-only in the agent.yaml pod; /workspace (HOME,
# cwd) and /tmp are emptyDirs. A driver-started child pod has no
# volumes (storage on the lease is 264f), so the image ships /workspace
# and /tmp mode 1777: there the writes land in the container's own
# layer, which dies with the pod like the emptyDir does. /etc/passwd
# names uid 65534 so tmux, git and node can resolve the user; SHELL is
# bash for tmux's default-shell.
# Layers (talos-config-9g6m): pi's closure (node, the bulk) and the
# tools' closure sit in their own layers below the image layer, so a
# rebuild that changes only the child binary or the entrypoint re-pulls
# megabytes, not the whole ~450 MB — a fresh digest used to cost every
# node ~45 s of image pull on its first birth (2026-10-18). A layer
# changes only when what it holds does: pi on a `nixpkgs-pi` bump, the
# tools on a nixpkgs bump.
# x86_64-linux only; from a laptop:
#
#   k8s/apps/sandbox/build.sh   # HUB_BUILDER=mar@nixos: build, push ghcr.io/marnyg/sandbox-agent:<rev>
{ pkgs, lib, self, nix2container, pi, actors }:
let
  entrypoint = pkgs.writeShellApplication {
    name = "agent-entrypoint";
    runtimeInputs = [ pi pkgs.tmux pkgs.coreutils ];
    text = ''
      export HOME=''${HOME:-/workspace}
      # A lease: the child beat owns the process from here. pi's
      # provider/model ride SAP_PI_ARGS (comma-separated, cmd/child);
      # the workspace is HOME, as in the other modes.
      if [ -n "''${SAP_INTRO:-}" ]; then
        mkdir -p "$HOME"
        cd "$HOME"
        export SAP_PI_ARGS="''${SAP_PI_ARGS:---provider,openrouter,--model,''${AGENT_MODEL:-anthropic/claude-sonnet-4.6}}"
        export SAP_WORKDIR="''${SAP_WORKDIR:-$HOME}"
        exec /usr/local/bin/child
      fi
      # tmux's socket under HOME, not /tmp: HOME is writable by
      # definition, /tmp's mode is the volume's business.
      export TMUX_TMPDIR=''${TMUX_TMPDIR:-$HOME/.tmux}
      mkdir -p "$HOME" "$TMUX_TMPDIR"
      cd "$HOME"
      model=''${AGENT_MODEL:-anthropic/claude-sonnet-4.6}

      # </dev/null: the pod's stdin is a pipe the runtime never closes,
      # and pi prepends piped stdin to the prompt — waiting for EOF
      # forever (first kata run, 2026-10-10).
      if [ -n "''${AGENT_PROMPT:-}" ]; then
        exec pi -p --no-session --provider openrouter --model "$model" -- "$AGENT_PROMPT" </dev/null
      fi

      tmux -f /etc/tmux.conf new-session -d -s agent -x 200 -y 50 -c "$HOME" \
        "pi --provider openrouter --model $(printf %q "$model")"
      echo "agent: interactive; attach with: kubectl exec -it -n ai ''${HOSTNAME:-<pod>} -- tmux attach"
      trap 'echo "agent: SIGTERM; ending the tmux session" >&2; tmux kill-session -t agent 2>/dev/null' TERM INT
      while tmux has-session -t agent 2>/dev/null; do sleep 2; done
      echo "agent: tmux session ended"
    '';
  };
  root = pkgs.runCommand "sandbox-agent-root" { } ''
    mkdir -p $out/usr/local/bin $out/etc
    ln -s ${entrypoint}/bin/agent-entrypoint $out/usr/local/bin/agent-entrypoint
    ln -s ${actors}/bin/child $out/usr/local/bin/child
    echo 'nobody:x:65534:65534:nobody:/workspace:/bin/bash' > $out/etc/passwd
    echo 'nobody:x:65534:' > $out/etc/group
    # docs/tmux.md in pi: extended keys so Shift/Ctrl+Enter reach pi.
    cat > $out/etc/tmux.conf <<'EOF'
    set -g extended-keys on
    set -g extended-keys-format csi-u
    set -g default-terminal "tmux-256color"
    set -g history-limit 50000
    EOF
  '';
  # Their own copyToRoot entry: nix2container's perms refuse a tree
  # that shares a directory (/etc) with another entry.
  scratch = pkgs.runCommand "sandbox-agent-scratch" { } ''
    mkdir -p $out/workspace $out/tmp
  '';
  # /bin and /share/terminfo: what pi's tools and the agent's own
  # commands expect to find (bash provides /bin/sh; ncurses the
  # terminfo tmux-256color lives in).
  env = pkgs.buildEnv {
    name = "sandbox-agent-env";
    pathsToLink = [ "/bin" "/share/terminfo" ];
    paths = with pkgs; [
      bash
      coreutils
      findutils
      gnugrep
      gnused
      gawk
      diffutils
      git
      ripgrep
      fd
      curl
      jq
      less
      procps
      tmux
      ncurses
      pi
    ];
  };
  # The stable bulk, bottom to top: pi (node + the agent), then the
  # tools minus whatever pi's layers already carry. Popularity-split
  # inside each so a single changed dependency re-pulls little.
  piLayer = nix2container.buildLayer { deps = [ pi ]; maxLayers = 16; };
  toolsLayer = nix2container.buildLayer { deps = [ env ]; layers = [ piLayer ]; maxLayers = 16; };
in
nix2container.buildImage {
  name = "ghcr.io/marnyg/sandbox-agent";
  tag = self.shortRev or self.dirtyShortRev or "dev";
  layers = [ piLayer toolsLayer ];
  # What is left for the image's own layer: the entrypoint, the child
  # binary, /etc, the scratch dirs, the CA bundle.
  copyToRoot = [ pkgs.cacert root scratch env ];
  # Writable by uid 65534 when no volume covers them (a child pod).
  # The regex sees the STORE path (…-sandbox-agent-scratch/tmp), not
  # the path in the image.
  perms = [
    { path = scratch; regex = "/(workspace|tmp)$"; mode = "1777"; }
  ];
  config = {
    Entrypoint = [ "/usr/local/bin/agent-entrypoint" ];
    Env = [
      "PATH=/usr/local/bin:/bin"
      "SSL_CERT_FILE=/etc/ssl/certs/ca-bundle.crt"
      "HOME=/workspace"
      "TMUX_TMPDIR=/workspace/.tmux"
      "SHELL=/bin/bash"
      "TERM=xterm-256color"
      "TERMINFO_DIRS=/share/terminfo"
    ];
    User = "65534:65534";
    WorkingDir = "/workspace";
  };
}
