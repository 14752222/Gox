#!/usr/bin/env bash
# 交叉编译移动端 Go 库（android / harmony / ios）并 staging 成「平台子包」目录。
#
# 与 build-npm.sh 的分工：
#   build-npm.sh         → 桌面五平台 gox 可执行文件 → 主包 @goxjs/goxjs 的 binaries/
#   build-npm-mobile.sh  → 移动三平台 libgox 库      → 平台子包 @goxjs/goxjs-mobile-<platform>-<abi>（本脚本）
#
# 为什么移动端要单独一条、还要拆子包（M11 决策，详见 app/MOBILE-DISTRIBUTION.md）：
#   1. 移动端的窗口/输入/软键盘只存在于宿主侧，绕不开 JNI/NAPI/cgo，必须各平台自己的
#      NDK / Xcode / OHOS clang 参与 —— 所以"能编出哪些平台"是**按主机探测**的；
#   2. 移动端产物是 .so/.a（单平台 20–30MB），塞进主包会把每个桌面用户从 ~20MB
#      顶到 ~72MB —— 所以按「平台 × ABI」拆成独立子包，谁用谁装。
#
# 子包目录 = `npm/packages/<dirname>/package.json`（**committed 源文件**，本脚本只读它、
# 不改它）+ 本脚本拷进去的产物 + 生成的 manifest.json。所有产物落到
# `dist/npm-mobile-pkgs/<dirname>/`，CI 直接从这些目录 `npm publish`。
#
# 三个平台的产物与来源（复用既有脚本，不重复实现编译细节）：
#   android  scripts/build-android.sh          → dist/android/<abi>/libgox.so
#            （NDK 交叉编译，ELF e_machine 已由该脚本校验）
#   harmony  scripts/build-harmony.sh          → dist/harmony/<abi>/libgox.so
#            （GOOS=linux + OHOS clang/sysroot，musl；ELF 已校验）
#   ios      scripts/build-ios.sh --lib-only   → dist/ios/<sdk>-<arch>/libgox.a(+.h)
#            （c-archive，Xcode 工具链；仅 macOS 主机可产出）
#
# 子包布局（dirname 与内层文件）：
#
#   goxjs-mobile-android-arm64-v8a/        package.json + libgox.so   + manifest.json
#   goxjs-mobile-harmony-arm64-v8a/        package.json + libgox.so   + manifest.json
#   goxjs-mobile-ios-iphoneos-arm64/       package.json + libgox.a + libgox.h + manifest.json
#   goxjs-mobile-ios-iphonesimulator-arm64/ package.json + libgox.a + libgox.h + manifest.json
#
# manifest.json 是**扫描目录**生成的（不是编译过程记账），含 goxVersion + 每个产物的
# kind/sha256/size —— 壳工程取库前用它挡「版本错配 / 半包 / 被换」。
#
# 用法：
#   bash scripts/build-npm-mobile.sh                     # 探测主机，编出能编的全部平台 → dist/npm-mobile-pkgs/
#   bash scripts/build-npm-mobile.sh --platform android
#   bash scripts/build-npm-mobile.sh --platform android,harmony
#   bash scripts/build-npm-mobile.sh --abi arm64-v8a,x86_64
#   bash scripts/build-npm-mobile.sh --out dist/mobile-x
#   bash scripts/build-npm-mobile.sh --manifest-only     # 只重扫 $OUT 下已有子包并重写 manifest.json
#
# 退出码：全部请求的平台都成功 = 0；有平台在本机不可产出 = 1（末尾汇总原因，便于 CI
# 一眼区分"环境缺工具链"与"编译失败"）。
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

OUT="dist/npm-mobile-pkgs"
PLATFORM_ARG=""
ABI_ARG=""
MANIFEST_ONLY=0

HOST_OS=$(uname -s)

while [ $# -gt 0 ]; do
  case "$1" in
    --out) OUT=$2; shift 2 ;;
    --platform) PLATFORM_ARG=$2; shift 2 ;;
    --abi) ABI_ARG=$2; shift 2 ;;
    --manifest-only) MANIFEST_ONLY=1; shift ;;
    -h|--help) sed -n '1,52p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "error: 未知参数 $1（可用: --out --platform --abi --manifest-only）" >&2; exit 2 ;;
  esac
done

command -v go >/dev/null 2>&1 || { echo "error: go not found in PATH" >&2; exit 1; }
# node 用来读子包 package.json 的 name/version（write_manifest_for / pkg_version）。
command -v node >/dev/null 2>&1 || { echo "error: node not found in PATH（用来读子包 package.json）" >&2; exit 1; }

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

# 子包目录名 ← 平台 + ABI（包内 ABI 记号）。表是唯一真源，加平台/ABI 就在这里加一行。
subpkg_dirname() { # subpkg_dirname <platform> <abi>  → 打印 dirname，未知则返回 1
  case "$1/$2" in
    android/arm64-v8a)      echo "goxjs-mobile-android-arm64-v8a" ;;
    android/armeabi-v7a)    echo "goxjs-mobile-android-armeabi-v7a" ;;
    android/x86_64)         echo "goxjs-mobile-android-x86_64" ;;
    harmony/arm64-v8a)      echo "goxjs-mobile-harmony-arm64-v8a" ;;
    ios/iphoneos-arm64)     echo "goxjs-mobile-ios-iphoneos-arm64" ;;
    ios/iphonesimulator-arm64) echo "goxjs-mobile-ios-iphonesimulator-arm64" ;;
    *) return 1 ;;
  esac
}

# 在 $OUT 下建一个干净的子包目录，并把 committed 的 package.json / README.md 拷进去。
# package.json 是子包的"身份"（name/version/files/access），必须先在
# npm/packages/<dirname>/ 里存在 —— 否则这个平台就是没定义，直接报错而不是凑一个。
# README.md 不是 files 白名单项，但 npm 会自动收录包目录里的 README（包页面要用）。
init_subpkg() { # init_subpkg <dirname>
  local dir="$OUT/$1"
  local src="npm/packages/$1/package.json"
  if [ ! -f "$src" ]; then
    echo "error: 子包未定义：$src 不存在" >&2
    echo "       移动端子包按「平台 × ABI」拆开，新增一个得先在 npm/packages/<dirname>/" >&2
    echo "       放好 package.json（见 app/MOBILE-DISTRIBUTION.md）。" >&2
    return 1
  fi
  rm -rf "$dir"
  mkdir -p "$dir"
  cp "$src" "$dir/package.json"
  [ -f "npm/packages/$1/README.md" ] && cp "npm/packages/$1/README.md" "$dir/README.md"
}

# 扫描一个子包目录，生成 manifest.json。只认这三种文件名，顺序固定 ⇒ 可复现。
write_manifest_for() { # write_manifest_for <dirname> <platform> <abi>
  local dir="$OUT/$1" platform="$2" abi="$3"
  [ -d "$dir" ] || { echo "error: 子包目录不存在: $dir" >&2; return 1; }

  local -a files=()
  local cand
  # 注意：不能靠 nullglob —— "$dir/libgox.so" 这种没有通配符的字面路径不会被它去掉，
  # 必须显式判存在。
  for cand in "$dir/libgox.so" "$dir/libgox.a" "$dir/libgox.h"; do
    [ -f "$cand" ] && files+=("$cand")
  done
  if [ "${#files[@]}" -eq 0 ]; then
    echo "error: $dir 下没有任何 libgox 产物，拒绝写空清单" >&2
    return 1
  fi

  local pkgname; pkgname=$(node -p "require('./$dir/package.json').name")
  local ver; ver=$(pkg_version)

  {
    echo "{"
    echo "  \"schema\": 1,"
    echo "  \"package\": \"$pkgname\","
    echo "  \"platform\": \"$platform\","
    echo "  \"abi\": \"$abi\","
    echo "  \"goxVersion\": \"$ver\","
    echo "  \"generatedBy\": \"scripts/build-npm-mobile.sh\","
    echo "  \"artifacts\": ["
    local idx=0 last=$(( ${#files[@]} - 1 ))
    local f name kind sha size comma
    for f in "${files[@]}"; do
      name="${f##*/}"
      case "$name" in
        *.so) kind="c-shared" ;;
        *.a)  kind="c-archive" ;;
        *.h)  kind="header" ;;
        *)    kind="other" ;;
      esac
      sha=$(sha256_of "$f")
      size=$(wc -c < "$f" | tr -d ' \r')
      comma=","
      [ "$idx" = "$last" ] && comma=""
      printf '    {"path":"%s","kind":"%s","sha256":"%s","size":%s}%s\n' \
        "$name" "$kind" "$sha" "$size" "$comma"
      idx=$((idx + 1))
    done
    echo "  ]"
    echo "}"
  } > "$dir/manifest.json"

  echo "==> 清单 $dir/manifest.json"
  ls -lh "$dir/manifest.json" | sed 's/^/    /'
}

# ---- --manifest-only：只重扫已有子包（CI 收集多 runner 产物后 / 本地补扫） ----
if [ "$MANIFEST_ONLY" = 1 ]; then
  [ -d "$OUT" ] || { echo "error: 输出目录不存在: $OUT" >&2; exit 1; }
  n=0
  for d in "$OUT"/*/; do
    [ -d "$d" ] || continue
    dirname=$(basename "$d")
    # dirname 形如 goxjs-mobile-<platform>-<abi>；platform 取第一段，其余是 abi。
    rest=${dirname#goxjs-mobile-}
    platform=${rest%%-*}
    abi=${rest#*-}
    case "$platform" in
      android|harmony|ios) write_manifest_for "$dirname" "$platform" "$abi" ;;
      *) echo "warn: 跳过非子包目录 $dirname" >&2 ;;
    esac
    n=$((n + 1))
  done
  [ "$n" -gt 0 ] || { echo "error: $OUT 下没有子包目录" >&2; exit 1; }
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

# ABI 覆盖：逗号分隔，同时映射到 android / harmony 各自的写法。
# 注意 ios 的「abi」是 <sdk>-<arch>，不受 --abi 影响。
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

mkdir -p "$OUT"

# ---- android ----
build_android() {
  local abis=("$@")
  [ "${#abis[@]}" -gt 0 ] || abis=(arm64-v8a)
  local abi dirname
  for abi in "${abis[@]}"; do
    echo "==> [android] $abi"
    if ! dirname=$(subpkg_dirname android "$abi"); then
      echo "error: android/$abi 没有对应的子包定义" >&2
      return 1
    fi
    bash scripts/build-android.sh --abi "$abi" || return 1
    init_subpkg "$dirname" || return 1
    cp "dist/android/$abi/libgox.so" "$OUT/$dirname/libgox.so" || return 1
    ls -lh "$OUT/$dirname/libgox.so" | sed 's/^/    /'
  done
}

# ---- harmony ----
build_harmony() {
  local abis=("$@")
  [ "${#abis[@]}" -gt 0 ] || abis=(arm64)
  local abi pkg_abi dirname
  for abi in "${abis[@]}"; do
    # 子包目录统一用 HAP 侧的名字（用户是往 entry/libs/<abi>/ 拷的）：
    # arm64 → arm64-v8a。
    pkg_abi="$abi"
    case "$abi" in
      arm64) pkg_abi=arm64-v8a ;;
      arm)   pkg_abi=armeabi-v7a ;;
    esac
    echo "==> [harmony] $abi (子包 abi $pkg_abi)"
    if ! dirname=$(subpkg_dirname harmony "$pkg_abi"); then
      echo "error: harmony/$pkg_abi 没有对应的子包定义" >&2
      return 1
    fi
    bash scripts/build-harmony.sh --abi "$abi" || return 1
    init_subpkg "$dirname" || return 1
    cp "dist/harmony/$abi/libgox.so" "$OUT/$dirname/libgox.so" || return 1
    ls -lh "$OUT/$dirname/libgox.so" | sed 's/^/    /'
  done
}

# ---- ios ----
# 只有 macOS 能跑：c-archive 依赖 Xcode 的 clang/sysroot。编译细节复用
# build-ios.sh --lib-only（同一份 CGO_CFLAGS/LDFLAGS，避免两处漂移）。
# 一个 SDK 一个子包（iphoneos-arm64 / iphonesimulator-arm64）。
build_ios() {
  local sdk_name sdk_flag abi dirname
  for sdk_name in iphoneos iphonesimulator; do
    case "$sdk_name" in
      iphonesimulator) sdk_flag="--sim" ;;
      iphoneos)        sdk_flag="--device" ;;
    esac
    abi="$sdk_name-arm64"
    echo "==> [ios] $abi"
    if ! dirname=$(subpkg_dirname ios "$abi"); then
      echo "error: ios/$abi 没有对应的子包定义" >&2
      return 1
    fi
    GOX_IOS_TARGET="${sdk_name#iphone}" bash scripts/build-ios.sh --lib-only "$sdk_flag" --arch arm64 || return 1
    init_subpkg "$dirname" || return 1
    cp "dist/ios/$abi/libgox.a" "$OUT/$dirname/libgox.a" || return 1
    cp "dist/ios/$abi/libgox.h" "$OUT/$dirname/libgox.h" || return 1
    ls -lh "$OUT/$dirname"/libgox.* | sed 's/^/    /'
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

# 为本次 staging 的每个子包写清单（用 package.json 反推 platform/abi）
for d in "$OUT"/*/; do
  [ -d "$d" ] || continue
  dirname=$(basename "$d")
  [ -f "$d/libgox.so" ] || [ -f "$d/libgox.a" ] || continue   # 跳过空目录
  rest=${dirname#goxjs-mobile-}
  platform=${rest%%-*}
  abi=${rest#*-}
  write_manifest_for "$dirname" "$platform" "$abi"
done

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
