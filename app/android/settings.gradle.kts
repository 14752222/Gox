// Gox Android 壳工程 —— 单模块 (:app)。
//
// 这里只做两件事: 把 libgox.so 装进 APK; 提供一个"最小宿主"(SurfaceView +
// Choreographer + 触摸)把 Go 引擎驱动起来。引擎与内核一份代码都不在这里。
//
// libgox.so 由 `bash scripts/build-android.sh` 产出, 手工拷到
// app/src/main/jniLibs/arm64-v8a/ 下再构建 (见 README)。
pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
    }
}

rootProject.name = "gox-android"
include(":app")
