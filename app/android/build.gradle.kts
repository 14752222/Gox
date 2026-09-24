// 根构建脚本: 只声明插件版本, 不配置任何模块。
//
// 版本组合是实测过的搭配 (Gradle 8.9 + AGP 8.6.1 + Kotlin 1.9.24 + JDK 17);
// 换 AGP 前先确认它与 compileSdk 的对应关系 (compileSdk 35 需要 AGP >= 8.6)。
plugins {
    id("com.android.application") version "8.6.1" apply false
    id("org.jetbrains.kotlin.android") version "1.9.24" apply false
}
