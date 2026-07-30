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

      perSystem = { pkgs, self', ... }:
        let
          sshKey = "$HOME/.ssh/id_ed25519";
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
            src = ./config-server;
            # git add new packages BEFORE recomputing this hash. Flakes only
            # see tracked files, so an untracked directory is invisible to
            # `go mod vendor` and its imports get silently left out of the
            # vendor dir — the build then fails with "import lookup disabled
            # by -mod=vendor" for a module that go.mod clearly requires.
            # Worse, the vendor derivation is fixed-output: nix reuses any
            # store path matching the hash, so a stale-but-matching vendor
            # dir survives `go mod tidy`. Force a recompute by setting a
            # bogus hash and reading nix's "got:" line.
            vendorHash = "sha256-Jm1E850P4cnxZEUBgOD99nUonyKrp2rkGcUW63ODu2w=";
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
            program = toString (pkgs.writeShellScript "encrypt-secrets" ''
              set -euo pipefail
              cd "$(git rev-parse --show-toplevel)/talos"
              # Cluster secrets are additionally encrypted to the
              # wallet-derived age recipient (public, committed — derive
              # with `wgping -age-recipient -sig <unseal-sig>`) so the
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
              find clusters -type f \( -name 'secrets.yaml' -o -name 'sealed-secrets.yaml' \) | while IFS= read -r f; do
                # shellcheck disable=SC2086
                ${pkgs.age}/bin/age -R "${sshKey}.pub" $FLY_RECIP -o "$f.age" "$f"
                echo "Encrypted $f"
              done
            '');
          };

          # nix run .#decrypt-secrets — decrypt all .age files
          apps.decrypt-secrets = {
            type = "app";
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
                ip=$($YQ '.ip' "$mac_dir/meta.yaml")

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
            '';
          };
        };
    };
}
