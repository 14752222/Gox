// Android 构建入口的"插件从哪来"声明 —— 没有这个文件 `gox build android`
// 会在 gradle 阶段直接失败（模块 build.gradle.kts 里的 plugins 没写版本，
// 裸跑时既解析不到 AGP 也没有仓库来源）。
//
// 版本组合与仓库壳工程 app/android/ 保持一致（Gradle 8.9 + AGP 8.6.1 +
// Kotlin 1.9.24 + JDK 17，compileSdk 35 需要 AGP >= 8.6）—— 换版本前先在
// 壳工程实测，两边一起改。
pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
    plugins {
        id("com.android.application") version "8.6.1"
        id("org.jetbrains.kotlin.android") version "1.9.24"
    }
}

dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
    }
}

rootProject.name = "__PROJECT_NAME__"
