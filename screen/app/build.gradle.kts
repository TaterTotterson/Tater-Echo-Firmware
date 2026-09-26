plugins {
    id("com.android.application")
}

val taterShowKeystorePath = System.getenv("TATER_SHOW_KEYSTORE_PATH")?.trim().orEmpty()
val taterShowKeyAlias = System.getenv("TATER_SHOW_KEY_ALIAS")?.trim().orEmpty()
val taterShowKeyPassword = System.getenv("TATER_SHOW_KEY_PASSWORD").orEmpty()
val taterShowStorePassword = System.getenv("TATER_SHOW_STORE_PASSWORD").orEmpty()

android {
    namespace = "com.tatertotterson.show"
    compileSdk = 37

    buildFeatures {
        buildConfig = true
    }

    defaultConfig {
        applicationId = "com.tatertotterson.show"
        // Fire OS 6 on stock Checkers is Android 7.1.2 (API 25). Keeping the
        // preview compatible lets us profile Amazon's original audio routing
        // before a replacement userspace removes that evidence.
        minSdk = 25
        targetSdk = 37
        versionCode = System.getenv("TATER_SHOW_VERSION_CODE")?.toIntOrNull()?.takeIf { it > 0 } ?: 1
        versionName = System.getenv("TATER_SHOW_VERSION_NAME")?.trim()?.ifBlank { null } ?: "0.1.0-dev"
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    signingConfigs {
        if (taterShowKeystorePath.isNotEmpty()) {
            create("taterRelease") {
                storeFile = file(taterShowKeystorePath)
                storePassword = taterShowStorePassword
                keyAlias = taterShowKeyAlias
                keyPassword = taterShowKeyPassword
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            signingConfig = signingConfigs.findByName("taterRelease")
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }
}

dependencies {
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.json:json:20250517")
}
