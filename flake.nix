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

          packages.config-server = pkgs.writeShellApplication {
            name = "config-server";
            runtimeInputs = [ (pkgs.python3.withPackages (ps: [ ps.pyyaml ])) self'.packages.talosctl ];
            text = ''
              cd "$(git rev-parse --show-toplevel)/talos"
              exec python3 ${./talos/serve-config.py} "$@"
            '';
          };

          # nix run .#encrypt-secrets — encrypt secrets patches and talosconfig files
          apps.encrypt-secrets = {
            type = "app";
            program = toString (pkgs.writeShellScript "encrypt-secrets" ''
              set -euo pipefail
              cd "$(git rev-parse --show-toplevel)/talos"
              # Encrypt talosconfig
              if [ -f talosconfig ]; then
                ${pkgs.age}/bin/age -R "${sshKey}.pub" -o talosconfig.age talosconfig
                echo "Encrypted talosconfig"
              fi
              # Encrypt cluster secrets
              find clusters -type f -name 'secrets.yaml' | while IFS= read -r f; do
                ${pkgs.age}/bin/age -R "${sshKey}.pub" -o "$f.age" "$f"
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

          # nix run .#apply [-- <mac>] — build and apply config to machines
          apps.apply = {
            type = "app";
            program = toString (pkgs.writeShellScript "apply" ''
              set -euo pipefail
              cd "$(git rev-parse --show-toplevel)/talos"

              YQ="${pkgs.yq-go}/bin/yq"
              FILTER="''${1:-}"

              apply_machine() {
                local mac_dir="$1"
                local mac meta ip config

                mac=$(basename "$mac_dir")
                meta="$mac_dir/meta.yaml"
                ip=$($YQ '.ip' "$meta")
                config=$($YQ '.config' "$meta")

                # Build patch args from meta.yaml patches list
                local patches=""
                while IFS= read -r p; do
                  [ -n "$p" ] && patches="$patches --patch @$p"
                done < <($YQ '.patches[]' "$meta")

                # Add machine-specific patch if it exists
                if [ -f "$mac_dir/patch.yaml" ]; then
                  patches="$patches --patch @$mac_dir/patch.yaml"
                fi

                echo "Applying to $mac ($ip) — $config"

                composed=$(${pkgs.talosctl}/bin/talosctl machineconfig patch "$config" $patches -o /dev/stdout)

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
