//go:build windows

// Package backend 按构建平台选择窗口后端: 此文件在 Windows 构建时
// 注册 gfx/win32。应用与 jsbuild 生成的 GUI 模板统一 blank import 本包,
// 无需关心目标平台。
package backend

import _ "github.com/14752222/Gox/gfx/win32"
