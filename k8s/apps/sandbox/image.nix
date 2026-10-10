# The agent harness image (talos-config-tj7c, ADR-0035's "null option"):
# pi — the coding agent the owner already drives — plus the tools it
# shells out to, as one non-root image a Job runs under RuntimeClass
# kata. No protocol binary yet: this is an agent as a plain workload;
# the child beat joins it in the agent-as-child follow-up.
#
# Two modes, chosen by the entrypoint from the environment:
#
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
# `pi` is pkgs.pi-coding-agent from its own nixpkgs pin (flake input
# `nixpkgs-pi`, nixos-unstable): the repo's nixpkgs predates the
# package and bumping it drags every Go and Rust build along;
# `nix flake update nixpkgs-pi` bumps pi alone.
#
# Root filesystem is read-only in the pod; /workspace (HOME, cwd) and
# /tmp are emptyDirs. /etc/passwd names uid 65534 so tmux, git and node
# can resolve the user; SHELL is bash for tmux's default-shell.
# x86_64-linux only; from a laptop:
#
#   k8s/apps/sandbox/build.sh   # HUB_BUILDER=mar@nixos: build, push ghcr.io/marnyg/sandbox-agent:<rev>
{ pkgs, lib, self, nix2container, pi }:
let
  entrypoint = pkgs.writeShellApplication {
    name = "agent-entrypoint";
    runtimeInputs = [ pi pkgs.tmux pkgs.coreutils ];
    text = ''
      export HOME=''${HOME:-/workspace}
      # tmux's socket under HOME, not /tmp: HOME is writable by
      # definition, /tmp's mode is the volume's business.
      export TMUX_TMPDIR=''${TMUX_TMPDIR:-$HOME/.tmux}
      mkdir -p "$HOME" "$TMUX_TMPDIR"
      cd "$HOME"
      model=''${AGENT_MODEL:-anthropic/claude-sonnet-4.6}

      if [ -n "''${AGENT_PROMPT:-}" ]; then
        exec pi -p --no-session --provider openrouter --model "$model" -- "$AGENT_PROMPT"
      fi

      tmux -f /etc/tmux.conf new-session -d -s agent -x 200 -y 50 \
        "pi --provider openrouter --model $(printf %q "$model")"
      echo "agent: interactive; attach with: kubectl exec -it -n ai ''${HOSTNAME:-<pod>} -- tmux attach"
      while tmux has-session -t agent 2>/dev/null; do sleep 5; done
      echo "agent: tmux session ended"
    '';
  };
  root = pkgs.runCommand "sandbox-agent-root" { } ''
    mkdir -p $out/usr/local/bin $out/etc $out/workspace $out/tmp
    ln -s ${entrypoint}/bin/agent-entrypoint $out/usr/local/bin/agent-entrypoint
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
in
nix2container.buildImage {
  name = "ghcr.io/marnyg/sandbox-agent";
  tag = self.shortRev or self.dirtyShortRev or "dev";
  copyToRoot = [ pkgs.cacert root env ];
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
