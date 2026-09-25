//
//  Gox-Bridging-Header.h
//  Gox
//
//  把 libgox.a 的导出函数暴露给 Swift。libgox.h 由 scripts/build-ios.sh
//  生成 (c-archive 附带产物), 构建前先跑脚本。
//
//  NativeHost 通道的导出 (gox_set_native_host / gox_resolve_native /
//  gox_report_*, 见 gfx/ios/libgox/main.go 的 //export) 也会被 cgo 生成进
//  libgox.h —— **不要在这里重复声明**: libgox.h 用的是 GoInt32/char* 风格
//  签名, 手写 const char* 版本会与之冲突 (编译器报 conflicting types)。
//  Swift 侧的调用入口见 NativeHost.swift。
//

#include "libgox.h"
