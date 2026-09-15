# libiroh_ffi.a for aarch64-linux-android (Mesh v3 P0.2 step 1,
# docs/mesh-v3-p0.2-android.md). The same pipeline as default.nix, just
# instantiated with nixpkgs' NDK-prebuilt cross stdenv: nixpkgs builds a
# cross rustc (std for the android target) from source — ~1 h once, cached
# in the store afterwards. Unfree (NDK), hence impure:
#
#   NIXPKGS_ALLOW_UNFREE=1 nix build --impure -f iroh-go/nix/android.nix -o result-android
#   → result-android/lib/libiroh_ffi.a  (+ .so, unused)
#
# Not wired into flake.nix: the flake is pure and the spike is scratch. If
# the gate passes, this becomes packages.iroh-ffi-android with the license
# accepted in the flake's nixpkgs config.
let
  flake = builtins.getFlake (toString ../..);
  pkgs0 = flake.inputs.nixpkgs.legacyPackages.${builtins.currentSystem};
  pkgs = import flake.inputs.nixpkgs {
    system = builtins.currentSystem;
    crossSystem = pkgs0.lib.systems.examples.aarch64-android-prebuilt;
    config = { allowUnfree = true; android_sdk.accept_license = true; };
  };
  irohGo = import ./. { inherit pkgs; lib = pkgs.lib; self = flake; };
in
irohGo.iroh-ffi
