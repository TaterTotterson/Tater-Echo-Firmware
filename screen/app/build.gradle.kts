plugins {
    id("com.android.application")
}

android {
    namespace = "com.tatertotterson.show"
    compileSdk = 37

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

    buildTypes {
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }
}

dependencies {
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.json:json:20250517")
}
