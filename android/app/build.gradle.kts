plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

// The build's identity. versionCode is the commit count on HEAD: it only
// grows, so a newer build always installs over an older one and the
// app's update check (Updater.kt) has something to compare with the
// published manifest (../publish.sh). versionName carries the commit for
// humans. -PmeshVersionCode / -PmeshVersionName override; without git
// (a tarball) the build falls back to the pre-count constants, which
// installs over nothing newer.
fun git(vararg args: String): String? = try {
    providers.exec { commandLine("git", *args) }.standardOutput.asText.get().trim().ifEmpty { null }
} catch (e: Exception) {
    null
}
val meshVersionCode: Int = (findProperty("meshVersionCode") as String?)?.toInt()
    ?: git("rev-list", "--count", "HEAD")?.toIntOrNull()
    ?: 2
val meshVersionName: String = (findProperty("meshVersionName") as String?)
    ?: git("rev-parse", "--short", "HEAD")?.let { "0.2-$it" }
    ?: "0.2"

android {
    namespace = "dev.marnyg.mesh"
    compileSdk = 34

    defaultConfig {
        applicationId = "dev.marnyg.mesh"
        // NVIDIA Shield (the first target) runs Android 9-11; 26 keeps
        // older TVs in range while allowing startForegroundService.
        minSdk = 26
        targetSdk = 33
        versionCode = meshVersionCode
        versionName = meshVersionName
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
