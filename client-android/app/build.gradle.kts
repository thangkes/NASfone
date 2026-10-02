import java.util.Properties

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

// Release signing key lives OUTSIDE the repo: ~/.nasfone-signing/keystore.properties
// (or the file named by NASFONE_KEYSTORE_PROPS). Without it, release builds fall back
// to the debug key so anyone can still build and install from source.
val signingProps = Properties().apply {
    val f = file(System.getenv("NASFONE_KEYSTORE_PROPS")
        ?: "${System.getProperty("user.home")}/.nasfone-signing/keystore.properties")
    if (f.isFile) f.inputStream().use { load(it) }
}

android {
    namespace = "com.nasfone.client"
    compileSdk = 36

    defaultConfig {
        applicationId = "com.nasfone.client"
        minSdk = 29
        targetSdk = 36
        versionCode = 10001
        versionName = "0.1.0-beta.1"
        ndk {
            abiFilters += "arm64-v8a"
            // -PwithX86 adds the emulator ABI for testing (needs the AAR built with -Emulator)
            if (project.hasProperty("withX86")) abiFilters += "x86_64"
        }
    }

    signingConfigs {
        if (signingProps.containsKey("storeFile")) {
            create("release") {
                storeFile = file(signingProps.getProperty("storeFile"))
                storePassword = signingProps.getProperty("storePassword")
                keyAlias = signingProps.getProperty("keyAlias")
                keyPassword = signingProps.getProperty("keyPassword")
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            signingConfig = signingConfigs.findByName("release") ?: signingConfigs.getByName("debug")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    lint {
        checkReleaseBuilds = false
    }
}

dependencies {
    // Go core (pairing, sign-in, WebDAV): scripts\build-client-android.ps1 builds it with gomobile.
    implementation(files("libs/nasfoneclient.aar"))
    // QR scanner for pairing invites (camera; no Google Play services needed)
    implementation("com.journeyapps:zxing-android-embedded:4.3.0")
    // The scanner calls ContextCompat for the camera permission but does not bring it along
    implementation("androidx.core:core:1.13.1")
}

kotlin {
    compilerOptions {
        jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)
    }
}
