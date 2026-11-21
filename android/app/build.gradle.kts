plugins {
    id("com.android.application")
}

android {
    namespace = "com.checknetwork.app"
    compileSdk = 35
    buildToolsVersion = "35.0.0"

    defaultConfig {
        applicationId = "com.checknetwork.app"
        minSdk = 26
        targetSdk = 35
        versionCode = 1
        // Keep the public app version aligned with the API/release source version.
        versionName = providers.fileContents(
            rootProject.layout.projectDirectory.file("../VERSION")
        ).asText.get().trim()
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    testOptions {
        unitTests.isIncludeAndroidResources = true
        unitTests.isReturnDefaultValues = false
    }

    lint {
        abortOnError = true
        checkReleaseBuilds = true
        htmlReport = true
        xmlReport = true
    }
}

dependencies {
    implementation("androidx.core:core:1.19.1")
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.robolectric:robolectric:4.18.1")
}
