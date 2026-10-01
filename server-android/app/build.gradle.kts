plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
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

    buildTypes {
        release {
            isMinifyEnabled = false
            // Tam ky bang debug key de cai thu; thay bang keystore rieng truoc khi phat hanh
            signingConfig = signingConfigs.getByName("debug")
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
