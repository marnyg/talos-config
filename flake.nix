{
  description = "Talos Kubernetes cluster configuration";

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

          # Wrapper that resolves machine info from machines.json
          # Usage: tc <mac> <talosctl-command> [args...]
          #   tc b0:41:6f:15:3b:8f dashboard
          #   tc b0:41:6f:15:3b:8f dmesg
          #   tc b0:41:6f:15:3b:8f services
          # If only one machine exists, the MAC can be omitted:
          #   tc dashboard
          packages.tc = pkgs.writeShellApplication {
            name = "tc";
            runtimeInputs = [ pkgs.jq self'.packages.talosctl ];
            text = ''
              cd "$(git rev-parse --show-toplevel)"
              MACHINES_FILE="machines.json"

              # If only one machine and first arg looks like a command, use it
              MACHINE_COUNT=$(jq 'length' "$MACHINES_FILE")
              if [ "$MACHINE_COUNT" -eq 1 ] && ! jq -e --arg k "''${1:-}" 'has($k)' "$MACHINES_FILE" > /dev/null 2>&1; then
                MAC=$(jq -r 'keys[0]' "$MACHINES_FILE")
              else
                MAC="''${1:?Usage: tc [mac] <command> [args...]}"
                shift
              fi

              IP=$(jq -r ".\"$MAC\".ip" "$MACHINES_FILE")
              CLUSTER=$(jq -r ".\"$MAC\".cluster" "$MACHINES_FILE")
              TALOSCONFIG="clusters/$CLUSTER/talosconfig"

              if [ "$IP" = "null" ]; then
                echo "Unknown machine: $MAC"
                echo "Available:"
                jq -r 'to_entries[] | "  \(.key) (\(.value.ip))"' "$MACHINES_FILE"
                exit 1
              fi

              exec talosctl -n "$IP" -e "$IP" --talosconfig "$TALOSCONFIG" "$@"
            '';
          };

          packages.config-server = pkgs.writeShellApplication {
            name = "config-server";
            runtimeInputs = [ pkgs.python3 self'.packages.talosctl ];
            text = ''
              exec python3 ${./serve-config.py} "$@"
            '';
          };

          # nix run .#encrypt-secrets — encrypt secrets patches and talosconfig files
          apps.encrypt-secrets = {
            type = "app";
            program = toString (pkgs.writeShellScript "encrypt-secrets" ''
              set -euo pipefail
              cd "$(git rev-parse --show-toplevel)"
              # Encrypt secrets and talosconfig files
              find clusters -type f \( -name 'secrets.yaml' -o -name 'talosconfig' \) | while IFS= read -r f; do
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
              cd "$(git rev-parse --show-toplevel)"
              find clusters -type f -name '*.age' | while IFS= read -r f; do
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
              cd "$(git rev-parse --show-toplevel)"

              MACHINES_FILE="machines.json"
              FILTER="''${1:-}"

              apply_machine() {
                local mac="$1"
                local ip config cluster patches talosconfig

                ip=$(${pkgs.jq}/bin/jq -r ".\"$mac\".ip" "$MACHINES_FILE")
                config=$(${pkgs.jq}/bin/jq -r ".\"$mac\".config" "$MACHINES_FILE")
                cluster=$(${pkgs.jq}/bin/jq -r ".\"$mac\".cluster" "$MACHINES_FILE")
                talosconfig="clusters/$cluster/talosconfig"

                # Build patch args
                patches=""
                while IFS= read -r p; do
                  patches="$patches --patch @$p"
                done < <(${pkgs.jq}/bin/jq -r ".\"$mac\".patches[]" "$MACHINES_FILE")

                echo "Applying config to $mac ($ip)..."
                echo "  role: $config"
                echo "  cluster: $cluster"

                composed=$(${pkgs.talosctl}/bin/talosctl machineconfig patch "$config" $patches -o /dev/stdout)

                ${pkgs.talosctl}/bin/talosctl \
                  -n "$ip" -e "$ip" \
                  --talosconfig "$talosconfig" \
                  apply-config --file <(echo "$composed")

                echo "Applied to $ip."
              }

              if [ -n "$FILTER" ]; then
                apply_machine "$FILTER"
              else
                for mac in $(${pkgs.jq}/bin/jq -r 'keys[]' "$MACHINES_FILE"); do
                  apply_machine "$mac"
                done
              fi
            '');
          };

          devenv.shells.default = {
            name = "talos-config";
            imports = [ ];
            packages = with pkgs; [
              self'.packages.talosctl
              self'.packages.tc
              self'.packages.config-server
              kubectl
              k9s
              age
            ];
            enterShell = ''
              for f in $(find clusters -name '*.age' 2>/dev/null); do
                out="''${f%.age}"
                if [ ! -f "$out" ] || [ "$f" -nt "$out" ]; then
                  age -d -i "${sshKey}" -o "$out" "$f"
                  echo "Decrypted $out"
                fi
              done
            '';
          };
        };
    };
}
