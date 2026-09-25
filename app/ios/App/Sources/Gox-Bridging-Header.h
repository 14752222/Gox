//
//  Gox-Bridging-Header.h
//  Gox
//
//  把 libgox.a 的导出函数暴露给 Swift。libgox.h 由 scripts/build-ios.sh
//  生成 (c-archive 附带产物), 构建前先跑脚本。
//

#include "libgox.h"
