//go:build ios

// Package backend 按构建平台选择窗口后端。**iOS 这一格刻意是空的**:
//
// 与 windows / linux / darwin 三个后端不同, iOS 的"窗口"是宿主 (app/ios 壳
// 工程) 的 UIView —— 必须由宿主先创建出画布与帧缓冲, 再把表面交给引擎。进程
// 启动时根本没有可注册的对象, 因此注册动作发生在 Swift 调 gox_init 的时候
// (gfx/mobile.Surface.Register, 装配在 gfx/ios/libgox)。
//
// 保留本文件是为了让"按平台选后端"这张表在 ios 上有明确的一格。没有它,
// ios 会落进 backend_other.go —— 那文件自称是"未支持的平台", 语义上是错的。
package backend
