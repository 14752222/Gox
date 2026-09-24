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

        // 两档都打进去:
        //   arm64-v8a —— 真机 (手机/平板);
        //   x86_64    —— 模拟器。x86 主机上的模拟器**原生**跑 x86_64, 而 arm64 是靠
        //                转译 (abilist 里虽然列着 arm64-v8a, 但软光栅 + 转译会慢到
        //                没法用)。开发验证走模拟器, 所以这一档不是可选项。
        ndk {
            abiFilters += listOf("arm64-v8a", "x86_64")
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
