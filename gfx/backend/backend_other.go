//go:build !windows && !linux && !darwin && !android && !ios

// Package backend 在未支持的平台 (BSD 等) 不注册任何窗口后端;
// render() 会返回 "no window backend available"。
//
// android / ios 各有专属的一格 (backend_android.go / backend_ios.go:
// 注册由宿主在各自入口侧触发), 不落在这里。
package backend
