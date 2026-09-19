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

      perSystem = { pkgs, self', inputs', lib, ... }:
        let
          sshKey = "$HOME/.ssh/id_ed25519";
          # In-house iroh Go binding (task talos-config-ow7): iroh-ffi 1.1.0
          # → uniffi-bindgen-go → iroh-go/iroh. See iroh-go/README.md.
          irohGo = import ./iroh-go/nix { inherit pkgs lib; self = self'; };
          # sovereign-actor Transport over iroh (task talos-config-0bc.2.6):
          # own Go module (iroh-transport/, replaces ../protocol + ../iroh-go);
          # protocol/ itself never imports iroh-go. See iroh-transport/README.md.
          irohTransport = import ./iroh-transport/nix { inherit pkgs lib; self = self'; };
          # The hub binary (cgo against iroh-go, -tags iroh) and its static
          # variant for the fly image. See config-server/nix/default.nix.
          configServer = import ./config-server/nix { inherit pkgs lib; self = self'; };
        in
        lib.mkMerge [
          {
            treefmt.config = {
              programs.nixpkgs-fmt.enable = true;
              programs.yamlfmt.enable = true;
            };
            packages.talosctl = pkgs.talosctl;

            # The hub. cgo (iroh) since talos-config-e8d; recipe, vendorHash
            # caveats and the static/musl variant live in config-server/nix.
            #   nix build .#config-server-bin      host build + test suite
            #   nix build .#config-server-static   musl, what the fly image ships (linux)
            #   nix build .#hub-image              the fly image (linux; fly/image.nix)
            packages.config-server-bin = configServer.bin;

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
            # nix build .#iroh-transport        — library module; checkPhase runs the
            #                                      two-actor handshake over iroh (direct +
            #                                      local relay) with -race
            # nix build .#iroh-transport-static — same suite under pkgsStatic (musl,
            #                                      -extldflags -static): the Talos-
            #                                      extension feasibility probe. Linux
            #                                      only — darwin pkgsStatic still links
            #                                      libSystem dynamically, so it proves
            #                                      nothing there.
            packages.iroh-transport = irohTransport.tests;
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

            # nix run .#kubeconfig — fetch the admin kubeconfig over the
            # mesh and point it at the control plane's mesh name.
            # `talosctl kubeconfig` writes cluster.controlPlane.endpoint
            # (the nebula address 10.42.218.125) as the server; that is
            # the kubelets' endpoint, not the admin's, and it is
            # unreachable from a desktop that is on the irohup tun but
            # not on nebula. The apiServer certSANs already carry the
            # mesh name (talos/clusters/homelab/cluster.yaml).
            apps.kubeconfig = {
              type = "app";
              meta.description = "Write $KUBECONFIG via talosctl, server rewritten to https://<cp>.mesh.internal:6443 (irohup tun; nebula not required)";
              program = toString (pkgs.writeShellScript "kubeconfig" ''
                set -euo pipefail
                root="$(git rev-parse --show-toplevel)"
                cd "$root/talos"
                YQ="${pkgs.yq-go}/bin/yq"
                KUBECONFIG="''${KUBECONFIG:-$root/kubeconfig}"
                TALOSCONFIG="''${TALOSCONFIG:-$root/talos/talosconfig}"
                export KUBECONFIG TALOSCONFIG

                # The control plane's declared name — the one machine
                # whose meta.yaml points at the controlplane base.
                cp=""
                for m in machines/*/meta.yaml; do
                  if [ "$($YQ '.config' "$m")" = "base/controlplane.yaml" ]; then
                    cp=$($YQ '.name' "$m")
                    break
                  fi
                done
                [ -n "$cp" ] || { echo "no machines/*/meta.yaml with config: base/controlplane.yaml" >&2; exit 1; }

                cluster=$($YQ '.cluster.clusterName' clusters/*/cluster.yaml | head -1)

                ${pkgs.talosctl}/bin/talosctl kubeconfig "$KUBECONFIG" --force
                ${pkgs.kubectl}/bin/kubectl config set-cluster "$cluster" \
                  --server "https://$cp.mesh.internal:6443" >/dev/null
                echo "Wrote $KUBECONFIG (server https://$cp.mesh.internal:6443)"
              '');
            };

            # nix run .#apply [-- <mac>] — fetch the hub-composed config
            # over the identity plane and apply it. Never composes locally:
            # the hub injects overlay identity, certSANs, and disk
            # encryption at serve time, so a locally composed config would
            # strip that state from a running machine. Everything is by
            # name on the irohup tun (359.8.2.4 / 359.9.1): the hub at
            # http://hub.mesh.internal (hub-http facet, admins only) and
            # each machine at <name>.mesh.internal — so it needs the
            # talos-mesh daemon up and this device enrolled as an admin;
            # nebula is not involved. Override the hub with APPLY_HUB.
            apps.apply = {
              type = "app";
              meta.description = "talosctl apply-config the hub-composed config to every machine (or one MAC) over the identity plane (irohup tun)";
              program = toString (pkgs.writeShellScript "apply" ''
                set -euo pipefail
                cd "$(git rev-parse --show-toplevel)/talos"

                YQ="${pkgs.yq-go}/bin/yq"
                HUB="''${APPLY_HUB:-http://hub.mesh.internal}"
                FILTER="''${1:-}"

                apply_machine() {
                  local mac_dir="$1"
                  local mac name host composed

                  mac=$(basename "$mac_dir")
                  name=$($YQ '.name // ""' "$mac_dir/meta.yaml")
                  # The node identifier apid on the endpoint dials for -n:
                  # on a control plane every -n is dialed as <target>:50000
                  # with SNI <target> (never short-circuited to itself), so
                  # it must be a name the machine resolves for itself and
                  # carries in its apid cert SANs — its hostname. That is
                  # the declared name unless meta.yaml says otherwise
                  # (cp1's generated hostname until t7b2).
                  host=$($YQ '.hostname // .name // ""' "$mac_dir/meta.yaml")

                  if [ -z "$name" ] || [ "$name" = "null" ]; then
                    echo "Skipping $mac — no name in meta.yaml" >&2
                    return 0
                  fi

                  echo "Applying to $mac ($name.mesh.internal, node $host) — hub-composed config from $HUB"

                  if ! composed=$(${pkgs.curl}/bin/curl -fsS --connect-timeout 10 "$HUB/config?mac=$mac"); then
                    echo "ERROR: could not fetch hub-composed config for $mac from $HUB." >&2
                    echo "Local composing is not a fallback: it would strip serve-time state (overlay identity, certSANs, disk encryption)." >&2
                    echo "Check: is the talos-mesh daemon up and enrolled as an admin (dscacheutil -q host -a name hub.mesh.internal)? Is the hub unsealed (/status)?" >&2
                    exit 1
                  fi

                  ${pkgs.talosctl}/bin/talosctl \
                    -e "$name.mesh.internal" -n "$host" \
                    --talosconfig talosconfig \
                    apply-config --file <(echo "$composed")
                }

                if [ -n "$FILTER" ]; then
                  mac_normalized=$(echo "$FILTER" | tr ':' '-')
                  apply_machine "machines/$mac_normalized"
                else
                  # One unreachable machine (declared but not installed,
                  # powered off) must not stop the rest: report and go on,
                  # fail at the end.
                  failed=""
                  for d in machines/*/; do
                    [ -f "$d/meta.yaml" ] || continue
                    if ! (apply_machine "''${d%/}"); then
                      echo "FAILED: $(basename "$d")" >&2
                      failed="$failed $(basename "$d")"
                    fi
                  done
                  if [ -n "$failed" ]; then
                    echo "apply failed for:$failed" >&2
                    exit 1
                  fi
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
          }
          # Linux-only (see the iroh-transport comment above): a plain mkIf on
          # the attribute would leave a defined-but-valueless option behind
          # that `nix flake check` trips over; mkMerge drops it cleanly.
          (lib.mkIf pkgs.stdenv.isLinux {
            packages.iroh-transport-static = irohTransport.static;
            # nix build .#p0relay-static — static musl smoke + p0relay (P0.1 probe)
            packages.p0relay-static = irohTransport.p0relayStatic;
            packages.config-server-static = configServer.static;
            # nix build .#hub-image; nix run .#hub-image.copyToRegistry —
            # the fly image (fly/image.nix; driver: fly/deploy.sh).
            packages.hub-image = import ./fly/image.nix {
              inherit pkgs lib;
              self = inputs.self;
              nix2container = inputs'.nix2container.packages.nix2container;
              configServer = configServer.static;
              irohRelay = configServer.irohRelayStatic;
            };
          })
        ];
    };
}
