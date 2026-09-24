//go:build !windows && !linux && !darwin && !android

// Package backend 在未支持的平台 (BSD / iOS 等) 不注册任何窗口后端;
// render() 会返回 "no window backend available"。
//
// android 有专属的一格 (backend_android.go: 注册由宿主在 JNI 侧触发), iOS 目前
// 还没有后端实现, 所以留在这里。
package backend
