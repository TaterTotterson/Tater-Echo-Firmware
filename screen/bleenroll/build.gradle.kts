plugins {
    id("com.android.application")
}

android {
    namespace = "com.tatertotterson.bleenroll"
    compileSdk = 37

    defaultConfig {
        applicationId = "com.tatertotterson.bleenroll"
        minSdk = 25
        targetSdk = 25
        versionCode = 1
        versionName = "1.0"
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    lint {
        // Echo Show support requires Android 7 behavior. This helper is
        // root-installed by the satellite firmware, not distributed by Play.
        disable += "ExpiredTargetSdkVersion"
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            isShrinkResources = false
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }
}
