plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "dev.marnyg.mesh"
    compileSdk = 34

    defaultConfig {
        applicationId = "dev.marnyg.mesh"
        // NVIDIA Shield (the first target) runs Android 9-11; 26 keeps
        // older TVs in range while allowing startForegroundService.
        minSdk = 26
        targetSdk = 33
        versionCode = 2
        versionName = "0.2"
        ndk { abiFilters += listOf("arm64-v8a") } // the only ABI the AAR carries
    }

    buildTypes {
        // Debug-signed on purpose for now (sideload-only distribution);
        // a release keystore is a deliberate later step because changing
        // the signature forces uninstall + re-enroll on every device.
        release {
            isMinifyEnabled = false
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }

    // Compress libgojni.so and extract it at install time. AGP's default for
    // minSdk >= 23 stores it uncompressed for direct mmap, which on 16 KB
    // page devices (Android 15+) requires 16 KB ELF/zip alignment; gomobile
    // emits 4 KB and the installer silently refuses ("The app wasn't
    // installed", P0.2 2026-09-16). build-aar.sh also asks the linker for
    // 16 KB max-page-size; this is the belt to that brace.
    packaging { jniLibs { useLegacyPackaging = true } }
}

dependencies {
    // The Go core: build with ../build-aar.sh (gomobile bind of
    // config-server/mobile, -tags iroh, libiroh_ffi.a linked statically).
    implementation(files("libs/mobile.aar"))
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.8.1")
}
