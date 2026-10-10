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
# Workspace and result (ivof.2 — ADR-0037's shape, no protocol yet):
#
#   AGENT_REPO          Forgejo `owner/name`. Set ⇒ the entrypoint clones
#                       it to /workspace/<name> (HOME stays /workspace, so
#                       pi's and tmux's dot-dirs are outside the tree),
#                       runs pi there, and on exit — pi done, tmux gone,
#                       or the Job's SIGTERM at the deadline — commits
#                       everything, pushes `agent/<AGENT_JOB>` and opens a
#                       PR whose body is the run's report (print mode's
#                       final message; the log tail if there is none).
#                       No diff ⇒ nothing pushed. Never a forced push: a
#                       retried run colliding on the branch fails loudly.
#                       Unset ⇒ the throwaway emptyDir, as before.
#   AGENT_REF           branch to clone and PR against; default the
#                       repo's default branch (what a bare clone checks out).
#   AGENT_JOB           the Job name (agent-run.sh sets it); names the branch.
#   FORGEJO_URL, FORGEJO_TOKEN
#                       from the sealed secret forgejo-agent (agent.yaml;
#                       the bot user `agent`, scoped write:repository +
#                       write:issue). The token reaches git through a
#                       credential helper and curl through a header file,
#                       never through argv.
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
    runtimeInputs = [ pi pkgs.tmux pkgs.coreutils pkgs.git pkgs.curl pkgs.jq ];
    text = ''
      export HOME=''${HOME:-/workspace}
      # tmux's socket under HOME, not /tmp: HOME is writable by
      # definition, /tmp's mode is the volume's business.
      export TMUX_TMPDIR=''${TMUX_TMPDIR:-$HOME/.tmux}
      mkdir -p "$HOME" "$TMUX_TMPDIR"
      cd "$HOME"
      model=''${AGENT_MODEL:-anthropic/claude-sonnet-4.6}
      # Pod name = <job>-<5 chars> when agent-run.sh did not say.
      job=''${AGENT_JOB:-''${HOSTNAME%-*}}
      repo=''${AGENT_REPO:-}
      workdir=$HOME
      base=""
      title="agent: $job"
      status=0
      report=$HOME/.agent-report   # pi -p's stdout: the final message
      log=$HOME/.agent-log         # pi -p's stderr
      touch "$report" "$log"

      # Workspace: a clone of AGENT_REPO under HOME, not HOME itself, so
      # `git add -A` never sweeps .pi/, .tmux/, .gitconfig into the result.
      if [ -n "$repo" ]; then
        : "''${FORGEJO_URL:?AGENT_REPO is set but FORGEJO_URL is not: secret forgejo-agent missing?}"
        : "''${FORGEJO_TOKEN:?AGENT_REPO is set but FORGEJO_TOKEN is not: secret forgejo-agent missing?}"
        export FORGEJO_USER=''${FORGEJO_USER:-agent}
        umask 077
        # The token stays off every argv: git asks the helper (ps shows
        # the helper's literal text; its own shell expands the variable),
        # curl reads the header from a file. Both under HOME, outside
        # the clone, gone with the pod.
        git config --global user.name "$FORGEJO_USER"
        git config --global user.email "$FORGEJO_USER@mesh.internal"
        # shellcheck disable=SC2016  # the helper's shell expands these, not ours
        git config --global credential.helper '!f() { echo "username=$FORGEJO_USER"; echo "password=$FORGEJO_TOKEN"; }; f'
        hdr=$HOME/.forgejo-header
        printf 'Authorization: token %s\n' "$FORGEJO_TOKEN" >"$hdr"
        umask 022
        workdir=$HOME/''${repo##*/}
        clone=(git clone --quiet)
        [ -z "''${AGENT_REF:-}" ] || clone+=(--branch "$AGENT_REF")
        "''${clone[@]}" "$FORGEJO_URL/$repo.git" "$workdir"
        base=$(git -C "$workdir" rev-parse --abbrev-ref HEAD)
        echo "agent: workspace $workdir = $repo@$base $(git -C "$workdir" rev-parse --short HEAD); result: branch agent/$job"
      fi
      cd "$workdir"

      api() { curl -sS -H @"$hdr" -H 'Content-Type: application/json' "$@"; }

      # Result: commit, push the branch, open the PR. Runs after pi in
      # every exit path; a late SIGTERM must not cut it short.
      finish() {
        trap "" TERM INT
        [ -n "$repo" ] || return 0
        cd "$workdir"
        git add -A
        if git diff --cached --quiet; then
          echo "agent: no changes in $workdir; nothing pushed"
          return 0
        fi
        git commit --quiet -m "agent: $job" -m "$title"
        # A plain push, never forced: if agent/$job exists from an earlier
        # run this is a fast-forward or it fails, and so does the Job.
        git push --quiet origin "HEAD:refs/heads/agent/$job"
        echo "agent: pushed agent/$job ($(git rev-parse --short HEAD))"
        {
          if [ -s "$report" ]; then
            cat "$report"
          else
            echo "No final message from pi; the last lines of its log:"
            echo
            echo '```'
            tail -n 60 "$log"
            echo '```'
          fi
          printf '\n---\nJob %s, pi exit %s, %s\n' "$job" "$status" "$(date -u +%FT%TZ)"
        } >"$HOME/.pr-body"
        jq -n --arg title "$title" --arg head "agent/$job" --arg base "$base" --rawfile body "$HOME/.pr-body" \
          '{title: $title, head: $head, base: $base, body: $body}' >"$HOME/.pr-request"
        code=$(api -o "$HOME/.pr-response" -w '%{http_code}' -X POST \
          --data-binary @"$HOME/.pr-request" "$FORGEJO_URL/api/v1/repos/$repo/pulls")
        case $code in
          201) echo "agent: PR $(jq -r .html_url "$HOME/.pr-response")" ;;
          409) echo "agent: a PR for agent/$job already exists:"
               api "$FORGEJO_URL/api/v1/repos/$repo/pulls?state=all" \
                 | jq -r --arg h "agent/$job" '.[] | select(.head.ref == $h) | .html_url' ;;
          *)   echo "agent: opening the PR failed (HTTP $code):" >&2
               cat "$HOME/.pr-response" >&2
               return 1 ;;
        esac
      }

      # </dev/null: the pod's stdin is a pipe the runtime never closes,
      # and pi prepends piped stdin to the prompt — waiting for EOF
      # forever (first kata run, 2026-10-10).
      if [ -n "''${AGENT_PROMPT:-}" ]; then
        title=$(printf '%s\n' "$AGENT_PROMPT" | grep -m1 . | cut -c1-200)
        # Not exec: finish must run after pi, including on the Job's
        # SIGTERM at activeDeadlineSeconds. bash runs a trap only once
        # the foreground command returns, so pi runs in the background
        # and `wait` — which a signal does interrupt — stands in for it.
        pi -p --no-session --provider openrouter --model "$model" -- "$AGENT_PROMPT" </dev/null >"$report" 2>"$log" &
        pid=$!
        trap 'echo "agent: SIGTERM; stopping pi" >&2; kill -TERM "$pid" 2>/dev/null' TERM INT
        wait "$pid" || status=$?
        if [ "$status" -gt 128 ]; then
          # wait was interrupted: give pi a moment, then reap it.
          for _ in $(seq 50); do kill -0 "$pid" 2>/dev/null || break; sleep 0.2; done
          kill -KILL "$pid" 2>/dev/null || true
          wait "$pid" || status=$?
        fi
        cat "$log" >&2
        cat "$report"
        echo "agent: pi exited $status"
        finish
        exit "$status"
      fi

      tmux -f /etc/tmux.conf new-session -d -s agent -x 200 -y 50 -c "$workdir" \
        "pi --provider openrouter --model $(printf %q "$model")"
      echo "agent: interactive; attach with: kubectl exec -it -n ai ''${HOSTNAME:-<pod>} -- tmux attach"
      trap 'echo "agent: SIGTERM; ending the tmux session" >&2; tmux kill-session -t agent 2>/dev/null' TERM INT
      while tmux has-session -t agent 2>/dev/null; do sleep 2; done
      echo "agent: tmux session ended"
      finish
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
