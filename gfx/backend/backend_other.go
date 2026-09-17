//go:build !windows && !linux && !darwin

// Package backend 在未支持的平台 (BSD 等) 不注册任何窗口后端;
// render() 会返回 "no window backend available"。
package backend
