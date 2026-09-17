//go:build darwin

// Package backend 按构建平台选择窗口后端: 此文件在 macOS 构建时
// 注册 gfx/cocoa (objc runtime 纯 syscall 桥)。
package backend

import _ "github.com/14752222/Gox/gfx/cocoa"
