import java.util.Properties

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

// 签名: keystore.properties（app/ 目录下）由 gox build/cert 流程生成或手工填写,
// 字段 storeFile/storePassword/keyAlias/keyPassword。没有它 release 不签名。
val keystoreProps = Properties()
val keystorePropsFile = file("keystore.properties")
if (keystorePropsFile.exists()) {
    keystorePropsFile.inputStream().use { keystoreProps.load(it) }
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
        // v1 不混淆: JNI 符号是"包名+类名"的字符串契约, 混淆器改类名会让
        // System.loadLibrary 之后的方法绑定直接失败 (UnsatisfiedLinkError)。
        // 真要开混淆, 必须给 com.gox.GoxRuntime 写 keep 规则。
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

    // libgox.so 走 src/main/jniLibs/arm64-v8a/ (默认目录), 由构建前的脚本拷贝。
    packaging {
        jniLibs {
            useLegacyPackaging = false
        }
    }
}

dependencies {
    // 折叠屏支持: `FoldingFeature` 是公开 API 里**唯一**能拿到铰链位置与姿态
    // 的东西 —— 平台层没有等价物 (Display.getRotation / Configuration 只说
    // "屏幕变了", 不说"折痕在哪")。
    //
    // 版本选 **1.4.0** (2025-05-20 stable) 而不是最新的 1.5.1:
    // 本工程的组合是 Gradle 8.9 + AGP 8.6.1 + compileSdk 35 (见
    // gradle.properties 与根 build.gradle.kts 的版本说明)。1.5.x 是为更新的
    // AGP/compileSdk 构建的, 在 8.6.1 上有踩到 "compiled against a newer
    // Android SDK" 一类报错的风险 —— 而我们要的 API (FoldingFeature 的
    // state / orientation / isSeparating / bounds / occlusionType) 在 1.4.0
    // 里全部齐备, 升级换不到任何东西。
    //
    // 要升 1.5.x 时, 请连 AGP + compileSdk 一起升, 然后跑一次
    // scripts/build-android.sh 之外的完整 gradle assemble 验证。
    implementation("androidx.window:window:1.4.0")
    // window-java 提供 `WindowInfoTrackerCallbackAdapter` —— 没有协程运行时
    // 时的回调桥 (core 的 `windowLayoutInfo(Activity)` 返回的 Flow 只能在
    // 协程里 collect, 而为一个回调拉进 kotlinx-coroutines 不划算)。
    // 版本必须与 window 一致, 否则运行期会 NoSuchMethodError。
    implementation("androidx.window:window-java:1.4.0")
    // window-java 的回调签名用的是 `androidx.core.util.Consumer` (不是 JDK 的
    // java.util.function.Consumer) —— 不显式声明的话 Kotlin 编译器报
    // "Cannot access class 'androidx.core.util.Consumer'. Check your module
    // classpath", 而**不会**告诉你缺哪个依赖。这是实测踩到的坑 (2026-10-01):
    // 报错只说"访问不到", 不看 window-java 的字节码很难想到是 androidx.core。
    implementation("androidx.core:core-ktx:1.13.1")
}
