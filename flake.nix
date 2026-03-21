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

          devenv.shells.default = {
            name = "talos-config";
            imports = [ ];
            packages = with pkgs; [
              self'.packages.talosctl
              self'.packages.config-server
              kubectl
              k9s
              age
            ];
            enterShell = ''
              for f in $(find systems -name '*.age' 2>/dev/null); do
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
