plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

val appVersionName = (project.findProperty("appVersionName") as String?) ?: "1.0.0"
val appVersionCode = ((project.findProperty("appVersionCode") as String?) ?: "1").toInt()
// Wariant "bez konfiguracji" (np. do wgrania przez MDM): adres serwera i klucz wpisane w APK.
// Podawane tylko przy budowie lokalnej (-PdefaultServer=... -PdefaultKey=...), nigdy w repozytorium.
val defaultServer = (project.findProperty("defaultServer") as String?) ?: ""
val defaultKey = (project.findProperty("defaultKey") as String?) ?: ""

android {
    namespace = "pl.metler.przywolania"
    compileSdk = 34

    defaultConfig {
        applicationId = "pl.metler.przywolania"
        minSdk = 26
        targetSdk = 34
        versionCode = appVersionCode
        versionName = appVersionName
        buildConfigField("String", "DEFAULT_SERVER", "\"${defaultServer.replace("\"", "")}\"")
        buildConfigField("String", "DEFAULT_KEY", "\"${defaultKey.replace("\"", "")}\"")
    }
    buildFeatures {
        buildConfig = true
    }

    signingConfigs {
        create("release") {
            val ks = System.getenv("ANDROID_KEYSTORE_FILE")
            if (ks != null && file(ks).exists()) {
                storeFile = file(ks)
                storePassword = System.getenv("ANDROID_KEYSTORE_PASSWORD")
                keyAlias = System.getenv("ANDROID_KEY_ALIAS")
                keyPassword = System.getenv("ANDROID_KEY_PASSWORD")
            } else {
                // brak klucza w sekretach: podpis kluczem debug (do testów)
                val dbg = signingConfigs.getByName("debug")
                storeFile = dbg.storeFile
                storePassword = dbg.storePassword
                keyAlias = dbg.keyAlias
                keyPassword = dbg.keyPassword
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            signingConfig = signingConfigs.getByName("release")
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
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
}
