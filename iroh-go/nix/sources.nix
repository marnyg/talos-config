# Pinned upstream sources for the iroh Go binding pipeline.
#
# Bump procedure (see ../README.md "Regenerating"): change `rev`, set every
# `hash`/`cargoHash` below it to lib.fakeHash, run `nix build .#iroh-go`,
# paste the "got:" hashes, run `nix run .#iroh-go-regen`, commit the
# regenerated Go package alongside.
{ fetchFromGitHub, lib }:
{
  # iroh-ffi is `publish = false` on crates.io — the git tag is the only
  # distribution. Its own Cargo.lock pins core iroh/iroh-relay 1.0.2 while
  # declaring `iroh = "1.0.0"`; we build with a PATCHED lock
  # (../regen/iroh-ffi.Cargo.lock: upstream's + `cargo update -p iroh
  # --precise 1.1.0`, same for iroh-relay) so core is 1.1.0 everywhere
  # (P0.4 pin; task talos-config-htt). `cargoHash` is for the patched lock.
  # See README "Versions" and "Regenerating" step 2.
  iroh-ffi = rec {
    version = "1.1.0";
    rev = "v${version}"; # = 5e451092dba0c1a09ee83ff6e5be37b1152a5c58
    src = fetchFromGitHub {
      owner = "n0-computer";
      repo = "iroh-ffi";
      inherit rev;
      hash = "sha256-6j4Ns2mUPbn0nR8KxBZGEAv/xTHxnnPIA26h4HL/kt4=";
    };
    cargoHash = "sha256-btNb6szMCjj9ZhvNveWb1/mdFtFQq46n0/NzbiRes5Q=";
    # core iroh version the patched lock resolves to (asserted in default.nix)
    core = "1.1.0";
    # uniffi version the ffi crate is built with (Cargo.lock). The bindgen
    # below MUST target the same 0.31.x line: uniffi checks a contract
    # version + per-function API checksums at Go init() and panics on
    # mismatch.
    uniffi = "0.31.2";
  };

  # NordSecurity's Go generator. Tag convention: v<gen>+v<uniffi>.
  uniffi-bindgen-go = rec {
    version = "0.7.1";
    uniffi = "0.31.0";
    rev = "v${version}+v${uniffi}"; # = 0b7fb4ceef12021bd7f790cc516fa9133e001813
    src = fetchFromGitHub {
      owner = "NordSecurity";
      repo = "uniffi-bindgen-go";
      inherit rev;
      hash = "sha256-ZoGxEWJKriGhe/nMpSbJF6pyyZQZLzdVervUrBzUM5k=";
    };
    # hash of the vendor dir for the TRIMMED lock (../regen/uniffi-bindgen-go.Cargo.lock)
    cargoHash = "sha256-KWDHby0dpa87yluRf7SUszmM4YqICYpv8powzrZQPyo=";
  };

  # The relay server, built from the core iroh repo at the 1.1.0 tag (nixpkgs
  # ships 0.95.1, pre-1.0 — unusable). Same core version as the ffi lib's
  # patched lock.
  iroh = rec {
    version = "1.1.0";
    rev = "v${version}"; # = fddf1a4ce29f92c6651eccff68fb366007b9be7d
    src = fetchFromGitHub {
      owner = "n0-computer";
      repo = "iroh";
      inherit rev;
      hash = "sha256-inEGT8MuNRlXwrFiQpFPybSOF+GkIJxMp2nv81xHhBI=";
    };
    cargoHash = "sha256-/1G+Rv/iNBkJL7NEioqozBoCUqYGPcSfhVCCTj8qfL8=";
  };
}
