#!/usr/bin/env bash
# 交叉编译移动端 Go 库（android / harmony / ios）并 staging 进 npm 包目录。
#
# 与 build-npm.sh 的分工：
#   build-npm.sh         → 桌面五平台 gox 可执行文件 → npm/binaries/
#   build-npm-mobile.sh  → 移动三平台 libgox 库      → npm/mobile/（本脚本）
#
# 为什么移动端要单独一条：桌面那条是 CGO_ENABLED=0 的纯 Go 交叉编译，任意主机
# 都能出全部目标；移动端的窗口/输入/软键盘只存在于宿主侧，绕不开 JNI/NAPI/cgo，
# 必须各平台自己的 NDK / Xcode / OHOS clang 参与。所以"能编出哪些平台"是**按主机
# 探测**的，而不是写死三平台全出。
#
# 三个平台的产物与来源（复用既有脚本，不重复实现编译细节）：
#   android  scripts/build-android.sh          → dist/android/<abi>/libgox.so
#            （NDK 交叉编译，ELF e_machine 已由该脚本校验）
#   harmony  scripts/build-harmony.sh          → dist/harmony/<abi>/libgox.so
#            （GOOS=linux + OHOS clang/sysroot，musl；ELF 已校验）
#   ios      scripts/build-ios.sh --lib-only   → dist/ios/<sdk>-<arch>/libgox.a
#            （c-archive，Xcode 工具链；仅 macOS 主机可产出）
#
# 包内布局（与 npm/package.json 的 files 白名单配套）：
#
#   mobile/
#     manifest.json                         # 版本 + 各产物 sha256/字节数清单
#     android/<abi>/libgox.so               # <abi> ∈ arm64-v8a | armeabi-v7a | x86_64
#     harmony/<abi>/libgox.so               # <abi> 用 HAP 侧目录名（arm64-v8a / x86_64）
#     ios/<sdk>-<arch>/{libgox.a,libgox.h}  # <sdk> ∈ iphonesimulator | iphoneos
#
# 清单是**扫描输出目录**生成的，不是靠编译过程记账 —— 所以 CI 里可以把多个
# runner 的产物（ubuntu 上的 android、macos 上的 ios）分别下载进同一个 mobile/
# 之后，用 --manifest-only 再补一次清单，条目自动齐全。
#
# 用法：
#   bash scripts/build-npm-mobile.sh                     # 探测主机，编出能编的全部平台 → dist/npm-mobile/
#   bash scripts/build-npm-mobile.sh --into-package      # 直接 staging 进 npm/mobile/（打包用）
#   bash scripts/build-npm-mobile.sh --platform android
#   bash scripts/build-npm-mobile.sh --platform android,harmony
#   bash scripts/build-npm-mobile.sh --abi arm64-v8a,x86_64
#   bash scripts/build-npm-mobile.sh --out dist/mobile-x
#   bash scripts/build-npm-mobile.sh --into-package --manifest-only   # 只重扫并重写清单
#
# 退出码：全部请求的平台都成功 = 0；有平台在本机不可产出 = 1（并在末尾汇总原因，
# 便于 CI 一眼看出"是环境缺工具链"而不是"编译失败"）。
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

OUT="dist/npm-mobile"
PLATFORM_ARG=""
ABI_ARG=""
MANIFEST_ONLY=0

HOST_OS=$(uname -s)

while [ $# -gt 0 ]; do
  case "$1" in
    --out) OUT=$2; shift 2 ;;
    --into-package) OUT="npm/mobile"; shift ;;
    --platform) PLATFORM_ARG=$2; shift 2 ;;
    --abi) ABI_ARG=$2; shift 2 ;;
    --manifest-only) MANIFEST_ONLY=1; shift ;;
    -h|--help) sed -n '1,54p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "error: 未知参数 $1（可用: --out --into-package --platform --abi --manifest-only）" >&2; exit 2 ;;
  esac
done

command -v go >/dev/null 2>&1 || { echo "error: go not found in PATH" >&2; exit 1; }

sha256_of() { # sha256_of <file>
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    certutil -hashfile "$(cygpath -w "$1" 2>/dev/null || echo "$1")" SHA256 | sed -n 2p | tr -d ' \r'
  fi
}

pkg_version() {
  if [ -f npm/package.json ] && command -v node >/dev/null 2>&1; then
    node -p "require('./npm/package.json').version" 2>/dev/null || true
  fi
}

# 扫描 $OUT 下所有移动端产物，生成 manifest.json。
# 只认三条固定路径模式（android/<abi>/*.so、harmony/<abi>/*.so、ios/<sdk>-<arch>/libgox.*），
# 顺序稳定 ⇒ 同一批产物生成的清单逐字节一致，CI 上可复现、可 diff。
write_manifest() {
  [ -d "$OUT" ] || { echo "error: 输出目录不存在: $OUT" >&2; return 1; }

  local -a files=()
  shopt -s nullglob
  files=("$OUT"/android/*/libgox.so "$OUT"/harmony/*/libgox.so \
         "$OUT"/ios/*/libgox.a "$OUT"/ios/*/libgox.h)
  shopt -u nullglob

  if [ "${#files[@]}" -eq 0 ]; then
    echo "error: $OUT 下没有找到任何 libgox 产物，拒绝写空清单" >&2
    return 1
  fi

  local ver
  ver=$(pkg_version)

  {
    echo "{"
    echo "  \"schema\": 1,"
    echo "  \"package\": \"@goxjs/goxjs\","
    echo "  \"goxVersion\": \"$ver\","
    echo "  \"generatedBy\": \"scripts/build-npm-mobile.sh\","
    echo "  \"artifacts\": ["
    local idx=0 last=$(( ${#files[@]} - 1 ))
    local f rel platform abi kind sha size comma
    for f in "${files[@]}"; do
      rel="${f#"$OUT"/}"                       # android/arm64-v8a/libgox.so
      platform="${rel%%/*}"                    # android
      abi=$(echo "$rel" | cut -d/ -f2)         # arm64-v8a / iphonesimulator-arm64
      case "${f##*/}" in
        *.so) kind="c-shared" ;;
        *.a)  kind="c-archive" ;;
        *.h)  kind="header" ;;
        *)    kind="other" ;;
      esac
      sha=$(sha256_of "$f")
      size=$(wc -c < "$f" | tr -d ' \r')
      comma=","
      [ "$idx" = "$last" ] && comma=""
      printf '    {"path":"mobile/%s","platform":"%s","abi":"%s","kind":"%s","sha256":"%s","size":%s}%s\n' \
        "$rel" "$platform" "$abi" "$kind" "$sha" "$size" "$comma"
      idx=$((idx + 1))
    done
    echo "  ]"
    echo "}"
  } > "$OUT/manifest.json"

  echo "==> 清单 $OUT/manifest.json"
  ls -lh "$OUT/manifest.json" | sed 's/^/    /'
}

# ---- --manifest-only：只重扫清单（CI 收集多 runner 产物后用） ----
if [ "$MANIFEST_ONLY" = 1 ]; then
  write_manifest
  echo
  echo "==> 产出："
  (cd "$OUT" && ls -lhR . | sed 's/^/    /')
  exit 0
fi

# ---- 请求的平台列表 ----
# 缺省 auto：探测主机能编哪些。显式 --platform 时按用户给的来（编不出也算失败，
# 免得 CI 里"看起来跑了"其实一个都没出）。
if [ -n "$PLATFORM_ARG" ]; then
  MODE=explicit
  IFS=',' read -r -a PLATFORMS <<< "$PLATFORM_ARG"
else
  MODE=auto
  PLATFORMS=()
  # android：NDK 在（Windows/Linux/macOS 都可能）
  if [ -n "${ANDROID_NDK_HOME:-}" ] || [ -d "H:/AndroidSDK/ndk" ] \
     || [ -d "${ANDROID_HOME:-}/ndk" ] || [ -d "${ANDROID_SDK_ROOT:-}/ndk" ] \
     || [ -d "$HOME/Android/Sdk/ndk" ]; then
    PLATFORMS+=(android)
  fi
  # harmony：DevEco SDK 在
  if [ -n "${DEVECO_SDK_HOME:-}" ] || [ -d "H:/DevEco Studio/sdk/default/openharmony/native" ]; then
    PLATFORMS+=(harmony)
  fi
  # ios：macOS 主机（Xcode）
  if [ "$HOST_OS" = "Darwin" ]; then
    PLATFORMS+=(ios)
  fi
fi

if [ "${#PLATFORMS[@]}" -eq 0 ]; then
  echo "error: 本机没有探测到任何可用的移动端工具链（NDK / DevEco SDK / macOS Xcode）" >&2
  echo "       显式指定: bash scripts/build-npm-mobile.sh --platform android" >&2
  exit 1
fi

# ABI 覆盖：逗号分隔，同时映射到 android / harmony 各自的写法
ANDROID_ABIS=()
HARMONY_ABIS=()
if [ -n "$ABI_ARG" ]; then
  IFS=',' read -r -a _abis <<< "$ABI_ARG"
  for a in "${_abis[@]}"; do
    case "$a" in
      # harmony 侧 --abi 用别名（arm64 / arm），android 侧用 ABI 目录名
      arm64-v8a|arm64) ANDROID_ABIS+=(arm64-v8a); HARMONY_ABIS+=(arm64) ;;
      armeabi-v7a|arm) ANDROID_ABIS+=(armeabi-v7a); HARMONY_ABIS+=(arm) ;;
      x86_64)          ANDROID_ABIS+=(x86_64);      HARMONY_ABIS+=(x86_64) ;;
      *) echo "error: 不支持的 ABI $a（可选 arm64-v8a / armeabi-v7a / x86_64）" >&2; exit 2 ;;
    esac
  done
fi

echo "==> 主机      $HOST_OS"
echo "==> 模式      $MODE"
echo "==> 平台      ${PLATFORMS[*]}"
echo "==> 输出目录  $OUT"

# staging 前先清空目标：与 build-npm.sh 同样的理由 —— 上一次的残留混进来会让
# "包里到底是不是这次的产物"无从判断，而这类脏包发出去只能靠 sha 才发现。
rm -rf "$OUT"
mkdir -p "$OUT"

# ---- android ----
build_android() {
  local abis=("$@")
  [ "${#abis[@]}" -gt 0 ] || abis=(arm64-v8a)
  local abi
  for abi in "${abis[@]}"; do
    echo "==> [android] $abi"
    bash scripts/build-android.sh --abi "$abi" || return 1
    mkdir -p "$OUT/android/$abi" || return 1
    cp "dist/android/$abi/libgox.so" "$OUT/android/$abi/libgox.so" || return 1
    ls -lh "$OUT/android/$abi/libgox.so" | sed 's/^/    /'
  done
}

# ---- harmony ----
build_harmony() {
  local abis=("$@")
  [ "${#abis[@]}" -gt 0 ] || abis=(arm64)
  local abi pkg_abi
  for abi in "${abis[@]}"; do
    # 包内目录统一用 HAP 侧的名字（用户是往 entry/libs/<abi>/ 拷的），
    # 所以 arm64 → arm64-v8a、arm → armeabi-v7a。
    pkg_abi="$abi"
    case "$abi" in
      arm64) pkg_abi=arm64-v8a ;;
      arm)   pkg_abi=armeabi-v7a ;;
    esac
    echo "==> [harmony] $abi (包内目录 $pkg_abi)"
    bash scripts/build-harmony.sh --abi "$abi" || return 1
    mkdir -p "$OUT/harmony/$pkg_abi" || return 1
    cp "dist/harmony/$abi/libgox.so" "$OUT/harmony/$pkg_abi/libgox.so" || return 1
    ls -lh "$OUT/harmony/$pkg_abi/libgox.so" | sed 's/^/    /'
  done
}

# ---- ios ----
# 只有 macOS 能跑：c-archive 依赖 Xcode 的 clang/sysroot。编译细节复用
# build-ios.sh --lib-only（同一份 CGO_CFLAGS/LDFLAGS，避免两处漂移）。
build_ios() {
  local sdk_name sdk_flag
  for sdk_name in iphonesimulator iphoneos; do
    case "$sdk_name" in
      iphonesimulator) sdk_flag="--sim" ;;
      iphoneos)        sdk_flag="--device" ;;
    esac
    echo "==> [ios] $sdk_name-arm64"
    GOX_IOS_TARGET="${sdk_name#iphone}" bash scripts/build-ios.sh --lib-only "$sdk_flag" --arch arm64 || return 1
    mkdir -p "$OUT/ios/$sdk_name-arm64" || return 1
    cp "dist/ios/$sdk_name-arm64/libgox.a" "$OUT/ios/$sdk_name-arm64/libgox.a" || return 1
    cp "dist/ios/$sdk_name-arm64/libgox.h" "$OUT/ios/$sdk_name-arm64/libgox.h" || return 1
    ls -lh "$OUT/ios/$sdk_name-arm64"/libgox.* | sed 's/^/    /'
  done
}

FAILED=()
for platform in "${PLATFORMS[@]}"; do
  case "$platform" in
    android) build_android "${ANDROID_ABIS[@]}" || FAILED+=("android") ;;
    harmony) build_harmony "${HARMONY_ABIS[@]}" || FAILED+=("harmony") ;;
    ios)
      if [ "$HOST_OS" != "Darwin" ]; then
        echo "error: ios 产物只能在 macOS 上编（c-archive 需要 Xcode）—— 本机是 $HOST_OS" >&2
        FAILED+=("ios")
      else
        build_ios || FAILED+=("ios")
      fi
      ;;
    *) echo "error: 未知平台 $platform" >&2; exit 2 ;;
  esac
done

write_manifest

echo
echo "==> 产出："
(cd "$OUT" && ls -lhR . | sed 's/^/    /')

if [ "${#FAILED[@]}" -gt 0 ]; then
  echo
  echo "error: 以下平台未能产出：${FAILED[*]}" >&2
  echo "       android 需要 Android NDK（--ndk 或 ANDROID_NDK_HOME）" >&2
  echo "       harmony 需要 DevEco SDK（--sdk 或 DEVECO_SDK_HOME）" >&2
  echo "       ios     需要 macOS + Xcode（Windows/Linux 上无法交叉编译出 c-archive）" >&2
  exit 1
fi
