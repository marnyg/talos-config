# Dev shell + repo checks:
#
#   nix develop --impure         the devenv shell (talosctl, quint, nickel, …)
#   nix fmt                      treefmt: nixpkgs-fmt + yamlfmt (repo defaults)
#   nix flake check --impure     canonical full check — formatting + every
#                                package/devShell derivation evaluates and builds
#
# `--impure` is required, not optional: the devenv flake-parts module
# reads $PWD (builtins.getEnv) to find the project root, and a pure
# evaluation fails with "devenv was not able to determine the current
# directory" while checking devShells.default. devenv's own guide
# (devenv.sh/guides/using-with-flakes) documents --impure as the way to
# use it from a flake; the alternatives (a `devenv-root` file input
# overridden per invocation, or dropping devShells from the outputs so
# `nix flake check` skips them) trade a flag for a hack, so the flag it
# is. Everything except the devShell evaluates pure.
{
  description = "Homelab Kubernetes cluster configuration";

  inputs = {
    treefmt-nix.url = "github:numtide/treefmt-nix";

    flake-parts.url = "github:hercules-ci/flake-parts";
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";

    devenv.url = "github:cachix/devenv";
    nix2container.url = "github:nlewo/nix2container";
    nix2container.inputs = { nixpkgs.follows = "nixpkgs"; };
  };

  outputs = { nixpkgs, flake-parts, ... }@inputs:
    flake-parts.lib.mkFlake { inherit inputs; } {
      imports = [
        inputs.devenv.flakeModule
        inputs.treefmt-nix.flakeModule
      ];
      systems = nixpkgs.lib.systems.flakeExposed;

      perSystem = { pkgs, self', lib, ... }:
        let
          sshKey = "$HOME/.ssh/id_ed25519";
          # In-house iroh Go binding (task talos-config-ow7): iroh-ffi 1.1.0
          # → uniffi-bindgen-go → iroh-go/iroh. See iroh-go/README.md.
          irohGo = import ./iroh-go/nix { inherit pkgs lib; self = self'; };
        in
        {
          treefmt.config = {
            programs.nixpkgs-fmt.enable = true;
            programs.yamlfmt.enable = true;
          };
          packages.talosctl = pkgs.talosctl;

          # buildGo126Module, not buildGoModule: the embedded nebula
          # (slackhq/nebula 1.11.0, for the mesh CA + lighthouse/relay)
          # requires go >= 1.26.0, while pkgs.go is still 1.25.x here.
          packages.config-server-bin = pkgs.buildGo126Module {
            pname = "config-server";
            version = "0.1.0";
            # config-server plus the real talos/mesh-policy.yaml: the
            # policy tests deliberately run against the shipped file
            # (../talos/mesh-policy.yaml), so the sandbox must carry it
            # — a fixture copy would un-guard the file (019ce97).
            src = nixpkgs.lib.fileset.toSource {
              root = ./.;
              fileset = nixpkgs.lib.fileset.unions [
                ./config-server
                ./talos/mesh-policy.yaml
              ];
            };
            modRoot = "config-server";
            # git add new packages BEFORE recomputing this hash. Flakes only
            # see tracked files, so an untracked directory is invisible to
            # `go mod vendor` and its imports get silently left out of the
            # vendor dir — the build then fails with "import lookup disabled
            # by -mod=vendor" for a module that go.mod clearly requires.
            # Worse, the vendor derivation is fixed-output: nix reuses any
            # store path matching the hash, so a stale-but-matching vendor
            # dir survives `go mod tidy`. Force a recompute by setting a
            # bogus hash and reading nix's "got:" line.
            vendorHash = "sha256-wffIVZiCnXrizVshV9W9zivCO11ts511eawyK6j0ABQ=";
          };

          # nix build .#iroh-go        — libiroh_ffi.{a,dylib|so} + generated Go
          #                               package, drift-checked against iroh-go/iroh
          # nix build .#iroh-go-smoke  — the smoke binary; its checkPhase runs the
          #                               direct and relay smoke (go test)
          # nix run   .#iroh-go-regen  — regenerate iroh-go/iroh after a pin bump
          packages.iroh-go = irohGo.iroh-go;
          packages.iroh-go-smoke = irohGo.smoke;
          packages.iroh-ffi = irohGo.iroh-ffi;
          # static-only lib dir for hand-run go builds:
          #   CGO_LDFLAGS="-L$(nix build .#iroh-ffi-static --print-out-paths)/lib"
          packages.iroh-ffi-static = irohGo.iroh-ffi-static;
          packages.iroh-relay = irohGo.iroh-relay;
          packages.uniffi-bindgen-go = irohGo.uniffi-bindgen-go;
          apps.iroh-go-regen = {
            type = "app";
            program = "${irohGo.regen}/bin/iroh-go-regen";
            meta.description = "Regenerate the iroh-go/iroh Go binding from iroh-ffi via uniffi-bindgen-go";
          };

          packages.config-server = pkgs.writeShellApplication {
            name = "config-server";
            runtimeInputs = [ pkgs.git ];
            text = ''
              exec ${self'.packages.config-server-bin}/bin/config-server \
                --root "$(git rev-parse --show-toplevel)/talos" "$@"
            '';
          };

          # nix run .#encrypt-secrets — encrypt secrets patches and talosconfig files
          apps.encrypt-secrets = {
            type = "app";
            meta.description = "Encrypt talos/talosconfig and every clusters/**/*secrets.yaml to .age (ssh key + wallet-derived age recipient)";
            program = toString (pkgs.writeShellScript "encrypt-secrets" ''
              set -euo pipefail
              cd "$(git rev-parse --show-toplevel)/talos"
              # Cluster secrets are additionally encrypted to the
              # wallet-derived age recipient (public, committed — derive
              # with `recover -age-recipient -sig <unseal-sig>`) so the
              # config server can decrypt them at unseal time. The ssh
              # key stays as a second recipient for break-glass.
              FLY_RECIP=""
              if [ -f age-recipient.txt ]; then
                FLY_RECIP="-r $(cat age-recipient.txt)"
              elif [ -f fly-recipient.txt ]; then
                echo "WARNING: using legacy fly-recipient.txt — migrate to age-recipient.txt" >&2
                FLY_RECIP="-r $(cat fly-recipient.txt)"
              fi
              # Encrypt talosconfig (SSH key only — fly never needs admin creds)
              if [ -f talosconfig ]; then
                ${pkgs.age}/bin/age -R "${sshKey}.pub" -o talosconfig.age talosconfig
                echo "Encrypted talosconfig"
              fi
              # Encrypt cluster secrets
              # Match by suffix, not by exact name: an exact-name list
              # silently skips any new secret file (worker-secrets.yaml
              # was the first), which fails open — plaintext left
              # unencrypted and only .gitignore standing between it and
              # a commit.
              find clusters -type f -name '*secrets.yaml' | while IFS= read -r f; do
                # shellcheck disable=SC2086
                ${pkgs.age}/bin/age -R "${sshKey}.pub" $FLY_RECIP -o "$f.age" "$f"
                echo "Encrypted $f"
              done
            '');
          };

          # nix run .#decrypt-secrets — decrypt all .age files
          apps.decrypt-secrets = {
            type = "app";
            meta.description = "Decrypt talos/talosconfig.age and every clusters/**/*.age with the ssh key";
            program = toString (pkgs.writeShellScript "decrypt-secrets" ''
              set -euo pipefail
              cd "$(git rev-parse --show-toplevel)/talos"
              for f in talosconfig.age $(find clusters -type f -name '*.age'); do
                [ -f "$f" ] || continue
                out="''${f%.age}"
                ${pkgs.age}/bin/age -d -i "${sshKey}" -o "$out" "$f"
                echo "Decrypted $out"
              done
            '');
          };

          # nix run .#edit-secrets -- <file> — decrypt, edit, re-encrypt
          apps.edit-secrets = {
            type = "app";
            meta.description = "Decrypt one secrets file, open it in $EDITOR, re-encrypt if changed";
            program = toString (pkgs.writeShellScript "edit-secrets" ''
              set -euo pipefail
              EDITOR="''${EDITOR:-nano}"
              FILE="''${1:?Usage: nix run .#edit-secrets -- <file>}"
              ENC="$FILE.age"

              if [ -f "$ENC" ]; then
                ${pkgs.age}/bin/age -d -i "${sshKey}" -o "$FILE" "$ENC"
              fi

              BEFORE=$(sha256sum "$FILE")
              $EDITOR "$FILE"
              AFTER=$(sha256sum "$FILE")

              if [ "$BEFORE" != "$AFTER" ] || [ ! -f "$ENC" ]; then
                ${pkgs.age}/bin/age -R "${sshKey}.pub" -o "$ENC" "$FILE"
                echo "Re-encrypted $ENC."
              else
                echo "No changes."
              fi
            '');
          };

          # nix run .#apply [-- <mac>] — fetch the hub-composed config
          # over the mesh and apply it. Never composes locally: the hub
          # injects overlay identity, certSANs, and disk encryption at
          # serve time, so a locally composed config would strip that
          # state from a running machine. Requires being on the mesh as
          # an admin device (the hub's overlay /config route refuses
          # others — nebup enrolls). Override the hub with APPLY_HUB
          # (default http://10.42.0.1, the hub's mesh address).
          apps.apply = {
            type = "app";
            meta.description = "talosctl apply-config the hub-composed config to every machine (or one MAC) over the mesh";
            program = toString (pkgs.writeShellScript "apply" ''
              set -euo pipefail
              cd "$(git rev-parse --show-toplevel)/talos"

              YQ="${pkgs.yq-go}/bin/yq"
              HUB="''${APPLY_HUB:-http://10.42.0.1}"
              FILTER="''${1:-}"

              apply_machine() {
                local mac_dir="$1"
                local mac ip composed

                mac=$(basename "$mac_dir")
                ip=$($YQ '.ip // ""' "$mac_dir/meta.yaml")

                # A machine declared but not yet installed has no known
                # mesh address (it is HKDF-derived hub-side, so it can
                # only be read off /status or mesh DNS after the first
                # serve). Skip it rather than let an empty -n abort the
                # run: machines/ is applied in directory order, so one
                # unaddressed newcomer would otherwise block every
                # machine sorting after it.
                if [ -z "$ip" ] || [ "$ip" = "null" ]; then
                  echo "Skipping $mac — no ip in meta.yaml (not installed yet?)" >&2
                  return 0
                fi

                echo "Applying to $mac ($ip) — hub-composed config from $HUB"

                if ! composed=$(${pkgs.curl}/bin/curl -fsS --connect-timeout 10 "$HUB/config?mac=$mac"); then
                  echo "ERROR: could not fetch hub-composed config for $mac from $HUB." >&2
                  echo "Local composing is not a fallback: it would strip serve-time state (overlay identity, certSANs, disk encryption)." >&2
                  echo "Check: are you on the mesh as an admin device (nebup)? Is the hub unsealed (/status)?" >&2
                  exit 1
                fi

                ${pkgs.talosctl}/bin/talosctl \
                  -n "$ip" -e "$ip" \
                  --talosconfig talosconfig \
                  apply-config --file <(echo "$composed")
              }

              if [ -n "$FILTER" ]; then
                mac_normalized=$(echo "$FILTER" | tr ':' '-')
                apply_machine "machines/$mac_normalized"
              else
                for d in machines/*/; do
                  [ -f "$d/meta.yaml" ] && apply_machine "''${d%/}"
                done
              fi
            '');
          };

          devenv.shells.default = {
            name = "talos-config";
            imports = [ ];
            packages = with pkgs; [
              self'.packages.talosctl
              self'.packages.config-server
              kubectl
              k9s
              age
              jq
              kubeseal
              flyctl
              # dig: verifying mesh DNS is a routine check now, and it has
              # to be aimed at the hub's overlay address (dig @10.42.0.1)
              # because the zone is served only on the overlay.
              dnsutils
              # nebula-cert: the mesh golden interop test
              # (nebderive.TestStockNebulaCertVerify) shells out to it and
              # skips when absent. Version must track the Sidero nebula
              # extension shipped by the factory (currently 1.10.3).
              nebula
              # quint: design-level model checking of the seal/enrollment/
              # approval lifecycles (verification/quint/, epic
              # talos-config-7wg). Bundles Apalache + JVM for `quint verify`.
              quint
              # nickel: contract-checks the real durable artifacts against
              # their snapshot invariants (verification/nickel/, currently
              # talos/mesh-policy.yaml) — the per-value complement to the
              # quint models' all-traces verification.
              nickel
            ];
            enterShell = ''
              cd_talos="$(git rev-parse --show-toplevel)/talos"
              for f in "$cd_talos"/talosconfig.age $(find "$cd_talos/clusters" -name '*.age' 2>/dev/null); do
                [ -f "$f" ] || continue
                out="''${f%.age}"
                if [ ! -f "$out" ] || [ "$f" -nt "$out" ]; then
                  age -d -i "${sshKey}" -o "$out" "$f"
                  echo "Decrypted $out"
                fi
              done

              export TALOSCONFIG="$cd_talos/talosconfig"
              export KUBECONFIG=$(git rev-parse --show-toplevel)/kubeconfig
            '';
          };
        };
    };
}
