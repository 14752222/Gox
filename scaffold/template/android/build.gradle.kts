import java.util.Properties

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

// 签名: keystore.properties 由 `gox build android` 自动生成（证书来自 gox cert
// 快捷生成或 gox.json 的 cert 段; 字段 storeFile/storePassword/keyAlias/keyPassword,
// 文件含密码已 gitignore）。没有它 release 包不签名, assembleDebug 不受影响。
val keystoreProps = Properties()
val keystorePropsFile = file("keystore.properties")
if (keystorePropsFile.exists()) {
    keystorePropsFile.inputStream().use { keystoreProps.load(it) }
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

    // 签名配置只在 keystore.properties 存在时注册, 避免"没有证书也要配密码"。
    signingConfigs {
        if (keystorePropsFile.exists()) {
            create("gox") {
                storeFile = file(keystoreProps.getProperty("storeFile"))
                storePassword = keystoreProps.getProperty("storePassword")
                keyAlias = keystoreProps.getProperty("keyAlias")
                keyPassword = keystoreProps.getProperty("keyPassword")
            }
        }
    }

    buildTypes {
        // 不混淆: JNI 符号是"包名+类名"的字符串契约, 混淆器改类名会让
        // System.loadLibrary 之后的方法绑定直接失败 (UnsatisfiedLinkError)。
        release {
            isMinifyEnabled = false
            if (keystorePropsFile.exists()) {
                signingConfig = signingConfigs.getByName("gox")
            }
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
