plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    // namespace 与 Kotlin 包名必须与 JNI 契约一致:
    // Go 侧导出的符号是 Java_com_gox_GoxRuntime_* —— 它由**包名 + 类名**拼出来。
    namespace = "com.gox"
    compileSdk = 35

    defaultConfig {
        // __APP_ID__ / __VERSION__ 由 `gox create` 的占位符替换写入,
        // 与 gox.json 的 appId / version 保持一致。
        applicationId = "__APP_ID__"
        minSdk = 24
        targetSdk = 35
        versionCode = 1
        versionName = "__VERSION__"

        // 两档都打进去:
        //   arm64-v8a —— 真机 (手机/平板);
        //   x86_64    —— 模拟器 (x86 主机上原生执行, 比 arm64 转译快得多)。
        ndk {
            abiFilters += listOf("arm64-v8a", "x86_64")
        }
    }

    buildTypes {
        // 不混淆: JNI 符号是"包名+类名"的字符串契约, 混淆器改类名会让
        // System.loadLibrary 之后的方法绑定直接失败 (UnsatisfiedLinkError)。
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

    // libgox.so 走 src/main/jniLibs/<abi>/ (默认目录), 构建前由脚本拷贝
    // (仓库根 scripts/build-android.sh 负责交叉编译出 libgox.so)。
    packaging {
        jniLibs {
            useLegacyPackaging = false
        }
    }
}
