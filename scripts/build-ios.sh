#!/usr/bin/env bash
# 交叉编译 Gox 的 iOS 侧 Go 静态库（libgox.a，buildmode=c-archive）。
#
# 与 build-android.sh 的关键差异: iOS 上宿主 (app/ios 壳工程) 与 Go **静态链**
# 进同一个二进制 —— 没有 c-shared, 没有动态库签名问题, App Store 审核对静态
# 链接的第三方代码也没有额外限制。cgo 在这条路上必须开 (CGO_ENABLED=1),
# 理由与 Android 相同: 跨语言边界绕不开 (见 build-android.sh 头部注释)。
#
# 产物落在 dist/ios/<sdk>/libgox.a + libgox.h (dist/ 已 gitignore)。
#
# 用法：
#     bash scripts/build-ios.sh                    # 真机 (iphoneos, arm64)
#     bash scripts/build-ios.sh --sim              # 模拟器 (iphonesimulator, arm64)
#     bash scripts/build-ios.sh --sim --arch x86_64  # Intel Mac 的模拟器
#
# 依赖: Xcode (xcode-select -p 指向完整 Xcode 而不是 CLT)。
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

SIM=0
ARCH=arm64
while [ $# -gt 0 ]; do
  case "$1" in
    --sim) SIM=1; shift ;;
    --arch) ARCH=$2; shift 2 ;;
    *) echo "error: 未知参数 $1" >&2; exit 2 ;;
  esac
done

if [ "$SIM" = 1 ]; then
  SDK_NAME=iphonesimulator
else
  SDK_NAME=iphoneos
fi

# ---- 读取项目级配置 gox.json（存在时）----
# 提取 appId/version/icon 导出到 GOX_APP_ID / GOX_VERSION / GOX_ICON,
# 供后续 xcodebuild/Info.plist 注入使用; 顺带校验源图标存在。
# gox.json 只做数据, 这里不执行其中任何字段。
PROJECT_DIR=${GOX_PROJECT_DIR:-$ROOT}
if [ -f "$PROJECT_DIR/gox.json" ]; then
  if command -v python3 >/dev/null 2>&1; then
    eval "$(python3 - "$PROJECT_DIR/gox.json" <<'PYEOF'
import json, os, sys
cfg = json.load(open(sys.argv[1]))
def q(s): return "'" + str(s).replace("'", "'\\''") + "'"
print("GOX_APP_ID=" + q(cfg.get("appId", "")))
print("GOX_VERSION=" + q(cfg.get("version", "")))
print("GOX_ICON=" + q(os.path.join(os.path.dirname(sys.argv[1]), cfg.get("icon", "assets/icon.png"))))
PYEOF
)"
    export GOX_APP_ID GOX_VERSION GOX_ICON
    echo "==> gox.json: appId=$GOX_APP_ID version=$GOX_VERSION icon=$GOX_ICON"
    if [ ! -f "$GOX_ICON" ]; then
      echo "error: gox.json 指向的源图标不存在: ${GOX_ICON}（先跑 gox icon）" >&2
      exit 1
    fi
  else
    echo "警告: 未安装 python3, 跳过 gox.json 读取" >&2
  fi
fi

SDK=$(xcrun -sdk "$SDK_NAME" --show-sdk-path)
CC=$(xcrun -sdk "$SDK_NAME" --find clang)
OUT="dist/ios/$SDK_NAME-$ARCH"

# min iOS 版本: 与壳工程 (app/ios/project.yml) 的部署版本保持一致 ——
# lib 的 min 版本高于宿主会直接链接失败。
MIN_IOS=15.0
#
# 用 -target 而不是老的 -miphoneos-version-min: 新 SDK (iOS 27) 对模拟器
# sysroot 与 min 版本的组合会报 -Wincompatible-sysroot (-Werror)。
if [ "$SIM" = 1 ]; then
  TARGET="$ARCH-apple-ios$MIN_IOS-simulator"
else
  TARGET="$ARCH-apple-ios$MIN_IOS"
fi

echo "==> 构建 libgox.a ($SDK_NAME, $TARGET)"
mkdir -p "$OUT"

env \
  GOOS=ios GOARCH="$ARCH" CGO_ENABLED=1 \
  CC="$CC" \
  CGO_CFLAGS="-isysroot $SDK -target $TARGET" \
  CGO_LDFLAGS="-isysroot $SDK -target $TARGET" \
  go build -buildmode=c-archive \
    -o "$OUT/libgox.a" \
    ./gfx/ios/libgox

# c-archive 会同时产出 libgox.h (含全部 //export 的声明)。
echo "==> 产物: $OUT/libgox.a + $OUT/libgox.h"
ls -lh "$OUT"

# 拷进壳工程 (project.yml 按这个路径 -force_load); 老产物先清掉, 免得模拟器
# 构建里混进真机库 (链接器对架构不匹配的成员只会告警一半, 排查很绕)。
APP_LIBS="$ROOT/app/ios/libs/$SDK_NAME-$ARCH"
rm -rf "$APP_LIBS"
mkdir -p "$APP_LIBS"
cp "$OUT/libgox.a" "$OUT/libgox.h" "$APP_LIBS/"
echo "==> 已拷入壳工程: app/ios/libs/$SDK_NAME-$ARCH/"
