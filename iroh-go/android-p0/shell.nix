# Build shell for the P0.2 spike APK on the NixOS builder
# (docs/mesh-v3-p0.2-android.md decision 2). Impure on purpose: the Android
# SDK is unfree and the spike is scratch.
#
#   cd iroh-go/android-p0
#   NIXPKGS_ALLOW_UNFREE=1 nix-shell --impure shell.nix
#   IROH_FFI_ANDROID_LIB=$(nix build --impure -f ../nix/android.nix --print-out-paths)/lib ./build-aar.sh
#   gradle --no-daemon assembleDebug
#   adb install -r app/build/outputs/apk/debug/app-debug.apk
let
  flake = builtins.getFlake (toString ../..);
  pkgs = import flake.inputs.nixpkgs {
    system = builtins.currentSystem;
    config = { allowUnfree = true; android_sdk.accept_license = true; };
  };
  sdk = (pkgs.androidenv.composeAndroidPackages {
    platformVersions = [ "34" ];
    buildToolsVersions = [ "34.0.0" ];
    includeNDK = true;
    ndkVersions = [ "27.0.12077973" ]; # same NDK nixpkgs' aarch64-android-prebuilt cross uses
    includeEmulator = false;
  }).androidsdk;
in
pkgs.mkShell {
  packages = [ sdk pkgs.gradle pkgs.jdk17 pkgs.go_1_26 ];
  ANDROID_HOME = "${sdk}/libexec/android-sdk";
  ANDROID_SDK_ROOT = "${sdk}/libexec/android-sdk";
  ANDROID_NDK_HOME = "${sdk}/libexec/android-sdk/ndk/27.0.12077973";
  JAVA_HOME = pkgs.jdk17.home;
  # aapt2 from Maven is dynamically linked against glibc paths NixOS lacks;
  # point gradle at the SDK's own.
  GRADLE_OPTS = "-Dorg.gradle.project.android.aapt2FromMavenOverride=${sdk}/libexec/android-sdk/build-tools/34.0.0/aapt2";
}
