//go:build linux

// Package backend 按构建平台选择窗口后端: 此文件在 Linux 构建时
// 注册 gfx/x11 (纯 Go X11 协议绑定 jezek/xgb, 无 cgo)。
package backend

import _ "github.com/14752222/Gox/gfx/x11"
