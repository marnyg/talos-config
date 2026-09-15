// Mesh v3 P0.2 spike APK (docs/mesh-v3-p0.2-android.md). Separate from
// the shipped android/ app on purpose: that one's CI publishes a rolling
// release on every push. Nothing here is meant to survive the gate.
plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "dev.marnyg.p0mesh"
    compileSdk = 34

    defaultConfig {
        applicationId = "dev.marnyg.p0mesh"
        minSdk = 26
        targetSdk = 33
        versionCode = 1
        versionName = "0.1"
        ndk { abiFilters += listOf("arm64-v8a") } // the only ABI the AAR carries
    }

    buildTypes {
        release { isMinifyEnabled = false }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions { jvmTarget = "17" }
}

dependencies {
    // gomobile bind of ../../mobile (package p0mobile): build with ../build-aar.sh
    implementation(files("libs/p0mobile.aar"))
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.8.1")
}
