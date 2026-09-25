#!/usr/bin/env bash
# iOS 完整打包: libgox.a 交叉编译 → xcodebuild 构建 Gox 壳工程 → 组装 dist/<name>.app。
#
# 与 build-android.sh 的关键差异: iOS 上宿主 (app/ios 壳工程) 与 Go **静态链**
# 进同一个二进制 —— 没有 c-shared, 没有动态库签名问题。cgo 在这条路上必须开
# (CGO_ENABLED=1), 理由与 Android 相同: 跨语言边界绕不开。
#
# 打包策略: 用户工程的 ios/ 骨架只有 Info.plist + AppIconSet (没有 xcodeproj),
# xcodebuild 构建的是仓库里的壳工程 app/ios/Gox.xcodeproj; 构建完成后把用户
# 工程"声明式"的部分 (bundle id / 显示名 / 版本 / 权限文案 / 图标 / 入口脚本)
# 合并进产物 .app。模拟器产物免签名, 可直接 simctl install。
#
# 用法:
#     bash scripts/build-ios.sh                          # 模拟器 (iphonesimulator, arm64)
#     bash scripts/build-ios.sh --sim                    # 同上 (兼容老参数)
#     bash scripts/build-ios.sh --device                 # 真机 (iphoneos, 需签名证书)
#     bash scripts/build-ios.sh --arch arm64,x86_64      # 模拟器 fat 库 (多架构 lipo)
#
# 环境变量 (gox build ios 会自动设置; 命令行参数优先):
#     GOX_PROJECT_DIR   用户项目目录 (默认仓库根; 读取其 gox.json / ios/ 骨架)
#     GOX_IOS_TARGET    simulator | device (默认 simulator)
#     GOX_IOS_ARCH      额外模拟器架构 (如 x86_64, 与命令行 --arch 合并)
#     GOX_ENTRY         打进 .app 的入口脚本 (默认 <project>/src/main.js)
#
# 产物: <project>/dist/<name>.app
#
# 依赖: Xcode (xcode-select -p 指向完整 Xcode 而不是 CLT)。
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

SIM=""                # 缺省模拟器 (免签名); 命令行 --sim/--device 显式指定
ARCHS=""
while [ $# -gt 0 ]; do
  case "$1" in
    --sim) SIM=1; shift ;;
    --device) SIM=0; shift ;;
    --arch) ARCHS=$2; shift 2 ;;
    *) echo "error: 未知参数 $1 (可用: --sim --device --arch <a[,b...]>)" >&2; exit 2 ;;
  esac
done
# 命令行没指定目标时读环境变量 (gox build ios 传进来)
if [ -z "$SIM" ]; then
  case "${GOX_IOS_TARGET:-simulator}" in device) SIM=0 ;; *) SIM=1 ;; esac
fi
# 架构: 命令行 --arch 优先; 否则环境变量 GOX_IOS_ARCH 追加到 arm64; 都没有就 arm64
if [ -n "$ARCHS" ]; then
  IFS=',' read -r -a ARCH_LIST <<< "$ARCHS"
else
  ARCH_LIST=(arm64)
  if [ -n "${GOX_IOS_ARCH:-}" ] && [ "$SIM" = 1 ]; then
    ARCH_LIST+=("$GOX_IOS_ARCH")
  fi
fi

if [ "$SIM" = 1 ]; then
  SDK_NAME=iphonesimulator
  TARGET_SUFFIX="-simulator"
else
  SDK_NAME=iphoneos
  TARGET_SUFFIX=""
  ARCH_LIST=("${ARCH_LIST[0]}")   # 真机 v1 只出单架构 (arm64), 简化签名与体积
fi

# ---- 读取项目级配置 gox.json（存在时）----
# 提取 appId/version/name/title/icon 导出到环境变量, 供产物 .app 组装使用;
# 顺带校验源图标存在。gox.json 只做数据, 这里不执行其中任何字段。
PROJECT_DIR=${GOX_PROJECT_DIR:-$ROOT}
if [ -f "$PROJECT_DIR/gox.json" ]; then
  if command -v python3 >/dev/null 2>&1; then
    eval "$(python3 - "$PROJECT_DIR/gox.json" <<'PYEOF'
import json, os, sys
cfg = json.load(open(sys.argv[1]))
def q(s): return "'" + str(s).replace("'", "'\\''") + "'"
print("GOX_APP_ID=" + q(cfg.get("appId", "")))
print("GOX_VERSION=" + q(cfg.get("version", "")))
print("GOX_NAME=" + q(cfg.get("name", "")))
print("GOX_TITLE=" + q(cfg.get("title", "")))
print("GOX_ICON=" + q(os.path.join(os.path.dirname(sys.argv[1]), cfg.get("icon", "assets/icon.png"))))
PYEOF
)"
    export GOX_APP_ID GOX_VERSION GOX_NAME GOX_TITLE GOX_ICON
    echo "==> gox.json: appId=$GOX_APP_ID name=$GOX_NAME version=$GOX_VERSION"
    if [ ! -f "$GOX_ICON" ]; then
      echo "error: gox.json 指向的源图标不存在: ${GOX_ICON}（先跑 gox create / gox icon）" >&2
      exit 1
    fi
  else
    echo "警告: 未安装 python3, 跳过 gox.json 读取" >&2
  fi
fi
APP_NAME=${GOX_NAME:-Gox}

SDK=$(xcrun -sdk "$SDK_NAME" --show-sdk-path)
CC=$(xcrun -sdk "$SDK_NAME" --find clang)

# min iOS 版本: 与壳工程 (app/ios/project.yml) 的部署版本保持一致 ——
# lib 的 min 版本高于宿主会直接链接失败。
MIN_IOS=15.0
#
# 用 -target 而不是老的 -miphoneos-version-min: 新 SDK (iOS 27) 对模拟器
# sysroot 与 min 版本的组合会报 -Wincompatible-sysroot (-Werror)。

# build_libgox <goarch>: 交叉编译单架构 libgox.a 到 dist/ios/<sdk>-<arch>。
build_libgox() {
  local goarch=$1
  local out="dist/ios/$SDK_NAME-$goarch"
  local target="$goarch-apple-ios$MIN_IOS$TARGET_SUFFIX"
  echo "==> 构建 libgox.a ($SDK_NAME, $target)"
  mkdir -p "$out"
  env \
    GOOS=ios GOARCH="$goarch" CGO_ENABLED=1 \
    CC="$CC" \
    CGO_CFLAGS="-isysroot $SDK -target $target" \
    CGO_LDFLAGS="-isysroot $SDK -target $target" \
    go build -buildmode=c-archive \
      -o "$out/libgox.a" \
      ./gfx/ios/libgox
}

# 多架构时先备份各单架构产物, 最后 lipo 合成 fat 库
SLICES=()
for a in "${ARCH_LIST[@]}"; do
  build_libgox "$a"
  SLICES+=("dist/ios/$SDK_NAME-$a/libgox.a")
done

# c-archive 会同时产出 libgox.h (含全部 //export 的声明)。
echo "==> libgox.a 架构: ${ARCH_LIST[*]}"

# ---- 拷进壳工程 (pbxproj 按这个路径 -force_load; 目录名沿用 iphonesimulator-arm64) ----
APP_LIBS="$ROOT/app/ios/libs/$SDK_NAME-${ARCH_LIST[0]}"
rm -rf "$APP_LIBS"
mkdir -p "$APP_LIBS"
cp "dist/ios/$SDK_NAME-${ARCH_LIST[0]}/libgox.h" "$APP_LIBS/"
if [ "${#SLICES[@]}" -gt 1 ]; then
  lipo -create "${SLICES[@]}" -output "$APP_LIBS/libgox.a"
  echo "==> 已合成 fat 库:"
  lipo -info "$APP_LIBS/libgox.a"
else
  cp "${SLICES[0]}" "$APP_LIBS/libgox.a"
fi
echo "==> 已拷入壳工程: app/ios/libs/$SDK_NAME-${ARCH_LIST[0]}/"

# ---- xcodebuild 构建壳工程 ----
XCPROJ="$ROOT/app/ios/Gox.xcodeproj"
SYMROOT="$ROOT/app/ios/build"
if [ ! -d "$XCPROJ" ]; then
  echo "error: 找不到壳工程 $XCPROJ (仓库不完整?)" >&2
  exit 1
fi

if [ "$SIM" = 1 ]; then
  echo "==> xcodebuild (iOS Simulator, ARCHS=${ARCH_LIST[*]})"
  # ARCHS 必须与 libgox.a 的架构对齐: generic destination 缺省会把工程支持的
  # 全部架构 (arm64 + x86_64) 各链一遍, 少编一边就会在另一个 slice 上链接失败。
  # -derivedDataPath: 中间产物/日志收进仓库 build/ 下, 不污染 ~/Library/Developer
  # 的全局 DerivedData (也免去沙箱/受限环境下写不了用户目录的问题)。
  xcodebuild \
    -project "$XCPROJ" -scheme Gox -configuration Debug \
    -destination 'generic/platform=iOS Simulator' \
    -derivedDataPath "$SYMROOT/DerivedData" \
    ARCHS="${ARCH_LIST[*]}" ONLY_ACTIVE_ARCH=NO \
    SYMROOT="$SYMROOT" build
  BUILT_APP="$SYMROOT/Debug-iphonesimulator/Gox.app"
else
  # 真机需要签名: 先探证书, 没有就给清晰指引, 别让 xcodebuild 的报错劝退人。
  if ! security find-identity -v -p codesigning 2>/dev/null | grep -q '"Apple Development\|"Apple Distribution\|"iOS Development\|"iOS Distribution'; then
    cat >&2 <<'EOF'
gox build ios: 未检测到可用的签名证书 (--device 需要真机签名)。
  1. Xcode → Settings → Accounts 登录 Apple ID (免费账号即可跑真机调试);
  2. 用 Xcode 打开 app/ios/Gox.xcodeproj, 在 Signing & Capabilities 勾选
     "Automatically manage signing" 并选择 Team;
  3. 或在打包时显式指定: xcodebuild ... DEVELOPMENT_TEAM=<TeamID>;
  4. 然后重跑 gox build ios --device。
模拟器验证不需要证书: gox build ios --simulator。
EOF
    exit 1
  fi
  echo "==> xcodebuild (iOS Device)"
  if ! xcodebuild \
      -project "$XCPROJ" -scheme Gox -configuration Debug \
      -destination 'generic/platform=iOS' \
      -derivedDataPath "$SYMROOT/DerivedData" \
      SYMROOT="$SYMROOT" build; then
    cat >&2 <<'EOF'
gox build ios: 真机构建失败 (十有八九是签名配置)。
  用 Xcode 打开 app/ios/Gox.xcodeproj → Signing & Capabilities 选好 Team,
  或给 xcodebuild 传 DEVELOPMENT_TEAM=<TeamID>; 再重跑 gox build ios --device。
EOF
    exit 1
  fi
  BUILT_APP="$SYMROOT/Debug-iphoneos/Gox.app"
fi

if [ ! -d "$BUILT_APP" ]; then
  echo "error: xcodebuild 完成但找不到产物 $BUILT_APP" >&2
  exit 1
fi

# ---- 组装 dist/<name>.app: 用户工程的声明式配置合并进壳产物 ----
DIST_DIR="$PROJECT_DIR/dist"
FINAL_APP="$DIST_DIR/$APP_NAME.app"
rm -rf "$FINAL_APP"
mkdir -p "$DIST_DIR"
cp -R "$BUILT_APP" "$FINAL_APP"

PLIST="$FINAL_APP/Info.plist"
if [ -f "$PROJECT_DIR/gox.json" ]; then
  # 1) 身份三件套: bundle id / 显示名 / 版本
  [ -n "${GOX_APP_ID:-}" ] && plutil -replace CFBundleIdentifier -string "$GOX_APP_ID" "$PLIST"
  [ -n "${GOX_TITLE:-}" ] && plutil -replace CFBundleDisplayName -string "$GOX_TITLE" "$PLIST"
  [ -n "${GOX_VERSION:-}" ] && plutil -replace CFBundleShortVersionString -string "$GOX_VERSION" "$PLIST"
  [ -n "${GOX_VERSION:-}" ] && plutil -replace CFBundleVersion -string "$GOX_VERSION" "$PLIST"

  # 2) 权限文案: 用户工程 ios/Info.plist 里 gox sync 注入的 NS*UsageDescription
  #    原样搬运; 壳工程自带的占位文案若用户未声明则删掉 —— 与 gox sync 的
  #    "未声明的权限一律不写入清单" 约定保持一致 (App Store 审核对未使用的
  #    权限描述键也会追问用途)。
  USER_PLIST="$PROJECT_DIR/ios/Info.plist"
  if [ -f "$USER_PLIST" ] && command -v python3 >/dev/null 2>&1; then
    python3 - "$USER_PLIST" "$PLIST" <<'PYEOF'
import plistlib, subprocess, sys
with open(sys.argv[1], "rb") as f:
    user = plistlib.load(f)
with open(sys.argv[2], "rb") as f:
    app = plistlib.load(f)
for k, v in user.items():
    if k.startswith("NS") and k.endswith("UsageDescription"):
        subprocess.run(["plutil", "-replace", k, "-string", str(v), sys.argv[2]], check=True)
for k in app:
    if k.startswith("NS") and k.endswith("UsageDescription") and k not in user:
        subprocess.run(["plutil", "-remove", k, sys.argv[2]], check=False)
PYEOF
  fi

  # 3) 图标: AppIconSet → 主屏图标资源 + CFBundleIcons 声明
  ICONSET="$PROJECT_DIR/ios/Assets.xcassets/AppIcon.appiconset"
  if [ -d "$ICONSET" ]; then
    icon_copy() {  # <源尺寸> <目标文件名(不含.png)>
      if [ -f "$ICONSET/AppIcon-$1.png" ]; then
        cp "$ICONSET/AppIcon-$1.png" "$FINAL_APP/$2.png"
      fi
    }
    icon_copy 58  AppIcon29x29@2x
    icon_copy 87  AppIcon29x29@3x
    icon_copy 80  AppIcon40x40@2x
    icon_copy 120 AppIcon60x60@2x
    icon_copy 180 AppIcon60x60@3x
    icon_copy 152 AppIcon76x76@2x
    icon_copy 167 AppIcon83.5x83.5@2x
    plutil -remove CFBundleIcons "$PLIST" >/dev/null 2>&1 || true
    plutil -insert CFBundleIcons -json '{
      "CFBundlePrimaryIcon": {
        "CFBundleIconFiles": [
          "AppIcon29x29@2x", "AppIcon29x29@3x", "AppIcon40x40@2x",
          "AppIcon60x60@2x", "AppIcon60x60@3x", "AppIcon76x76@2x",
          "AppIcon83.5x83.5@2x"
        ]
      }
    }' "$PLIST"
  fi
fi

# 4) 入口脚本: 壳从 bundle 根读 app.js (GoxViewController.startEngine)。
#    注意 iOS 壳用 gox_run_script(EvalVM) 执行, 不设 module base ——
#    入口必须是**单文件** (可以 import 内置 gx/* 模块, 不能 import 相对路径)。
ENTRY="${GOX_ENTRY:-$PROJECT_DIR/src/main.js}"
if [ -f "$ENTRY" ]; then
  # iOS 壳 v1 只支持单文件入口: 壳用 EvalVM 执行、无 module base, 相对路径
  # import 无法解析; 且入口会被拷成 bundle 根的 app.js, 若入口引 "./app.js"
  # 会解析回自身造成自导入。这里快速失败并给指引, 不带病出包。
  if grep -qE '(from[[:space:]]*|import[[:space:]]*)["'\'']\.\.?/' "$ENTRY"; then
    echo "error: iOS 入口必须是单文件 (不能 import 相对路径模块): $ENTRY" >&2
    echo "  iOS 壳无 module base, 无法解析 ./xxx 形式的导入。" >&2
    echo "  请把依赖内联进入口脚本, 或用 --entry / GOX_ENTRY 指定一个自包含脚本。" >&2
    exit 1
  fi
  cp "$ENTRY" "$FINAL_APP/app.js"
else
  echo "警告: 找不到入口脚本 $ENTRY, 保留壳工程自带的 app.js" >&2
fi

# 5) 产物已改动 (plist/资源/脚本), 模拟器侧重新做一次 ad-hoc 签名兜底
codesign --force -s - "$FINAL_APP" >/dev/null 2>&1 || true

echo "==> 完成: $FINAL_APP"
