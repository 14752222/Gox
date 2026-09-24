plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    // namespace 与 Kotlin 包名必须与 JNI 契约一致:
    // Go 侧导出的符号是 Java_com_gox_GoxRuntime_* —— 它由**包名 + 类名**拼出来,
    // 改这里就要同步改 gfx/android/libgox/main.go 里的 //export 名字。
    namespace = "com.gox"
    compileSdk = 35

    defaultConfig {
        applicationId = "com.gox.demo"
        minSdk = 24
        targetSdk = 35
        versionCode = 1
        versionName = "1.0"

        // 只出 arm64: libgox.so 目前只交叉编译了这一档 (见 scripts/build-android.sh 的 --abi)。
        // 要出 armeabi-v7a 就加 --abi armeabi-v7a 再把它加进这一行。
        ndk {
            abiFilters += listOf("arm64-v8a")
        }
    }

    buildTypes {
        // v1 不混淆: JNI 符号是"包名+类名"的字符串契约, 混淆器改类名会让
        // System.loadLibrary 之后的方法绑定直接失败 (UnsatisfiedLinkError)。
        // 真要开混淆, 必须给 com.gox.GoxRuntime 写 keep 规则。
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

    // libgox.so 走 src/main/jniLibs/arm64-v8a/ (默认目录), 由构建前的脚本拷贝。
    packaging {
        jniLibs {
            useLegacyPackaging = false
        }
    }
}
