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
        versionCode = 1
        versionName = "0.1"
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
}

dependencies {
    // The Go core: build with ../build-aar.sh (gomobile bind of
    // config-server/mobile).
    implementation(files("libs/mobile.aar"))
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.8.1")
}
