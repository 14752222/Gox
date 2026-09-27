//go:build !windows

// 统一注册当前平台的 GUI 窗口后端 (gfx/backend 按构建标签选择:
// linux→x11, darwin→cocoa, 其余平台无后端)。
package main

import _ "github.com/14752222/Gox/gfx/backend"
