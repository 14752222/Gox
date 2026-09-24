//go:build android

// Package backend 按构建平台选择窗口后端。**Android 这一格刻意是空的**:
//
// 与 windows / linux / darwin 三个后端不同, Android 的"窗口"是 Activity 的
// SurfaceView —— 必须由宿主先创建出画布与帧缓冲, 再把表面交给引擎。进程启动时
// 根本没有可注册的对象, 因此注册动作发生在 Kotlin 调 nativeInit 的时候
// (gfx/mobile.Surface.Register, 装配在 gfx/android/libgox)。
//
// 保留本文件是为了让"按平台选后端"这张表在 android 上有明确的一格。没有它,
// android 会落进 backend_other.go —— 那文件自称是"未支持的平台", 语义上是错的。
//
// 真机验证的真值表 (M1 验收时要逐条确认):
//
//	libgox.so 已加载 + nativeInit 已调 → window()/render() 能开窗口, 触摸能点
//	只加载了 .so 但没调 nativeInit      → render() 报 "no window backend available"
package backend
