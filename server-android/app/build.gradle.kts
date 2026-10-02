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
    namespace = "com.nasfone.server"
    compileSdk = 36

    defaultConfig {
        applicationId = "com.nasfone.server"
        minSdk = 29
        targetSdk = 36
        versionCode = 1
        versionName = "0.1.0"
        ndk { abiFilters += "arm64-v8a" }
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
    // Lõi Go (tsnet + WebDAV), build bằng: core> gomobile bind -target=android/arm64 -androidapi 29 -javapkg com.nasfone.core -o ../server-android/app/libs/nasfonecore.aar ./mobile
    implementation(files("libs/nasfonecore.aar"))
}

kotlin {
    compilerOptions {
        jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)
    }
}
