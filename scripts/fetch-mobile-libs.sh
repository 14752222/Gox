#!/usr/bin/env bash
# 从已安装的「平台子包」里取出预编译的移动端库，校验后拷进三平台壳工程。
#
# 与 build-npm-mobile.sh 是一出一进的一对：
#   build-npm-mobile.sh  → 编 libgox 并 staging 成平台子包 @goxjs/goxjs-mobile-<platform>-<abi>（发布端）
#   fetch-mobile-libs.sh → 从装好的子包取出产物放进壳工程（消费端，本脚本）
#
# 为什么是子包而不是主包（M11 决策，见 app/MOBILE-DISTRIBUTION.md）：
#   主包 @goxjs/goxjs 只发桌面二进制（~20MB）；移动端 .so/.a 单平台 20–30MB，
#   塞进主包会把每个桌面用户顶到 ~72MB。所以拆成按「平台 × ABI」的子包，谁用谁装。
#   **子包不写进主包的 dependencies / optionalDependencies** —— npm 默认会装
#   optionalDependencies，那等于没拆；必须由移动端消费者显式安装。
#
# 校验三件事（都属于"运行时才崩、编译期拦不住"的错，所以在这里挡住）：
#   1. manifest.json 的 goxVersion 必须等于引擎版本（cmd/gox/main.go 的 const version）
#      —— 防「新壳配旧引擎」：JNI/NAPI 契约不匹配，是装到设备上才炸的；
#   2. 每个产物的 sha256 必须与 manifest 记的一致 —— 防半包 / 被截断 / 被换；
#   3. 请求的 platform+abi 对应的子包必须已安装 —— 防"以为装上了，其实没有"。
#
# 用法：
#   npm i @goxjs/goxjs-mobile-android-arm64-v8a    # 先装要用的子包
#   bash scripts/fetch-mobile-libs.sh              # 默认取已安装的全部（三平台 arm64）
#   bash scripts/fetch-mobile-libs.sh --platform android --abi arm64-v8a
#   bash scripts/fetch-mobile-libs.sh --pkg-root <目录>   # 显式指定子包所在目录（测试用）
#   bash scripts/fetch-mobile-libs.sh --shell <壳工程根>  # 默认 app/
#   bash scripts/fetch-mobile-libs.sh --list       # 只列出解析到的子包
#   bash scripts/fetch-mobile-libs.sh --check-only # 只做校验与 sha 比对，不落盘
#
# 退出码：0 = 请求的产物全部校验通过并拷好；1 = 校验失败 / 子包缺失 / 版本错配。
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

PKG_ROOT=""
SHELL_DIR="$ROOT/app"
PLATFORM_ARG="all"
ABI="arm64-v8a"
CHECK_ONLY=0
LIST_ONLY=0

while [ $# -gt 0 ]; do
  case "$1" in
    --pkg-root) PKG_ROOT=$2; shift 2 ;;
    --shell) SHELL_DIR=$2; shift 2 ;;
    --platform) PLATFORM_ARG=$2; shift 2 ;;
    --abi) ABI=$2; shift 2 ;;
    --check-only) CHECK_ONLY=1; shift ;;
    --list) LIST_ONLY=1; shift ;;
    -h|--help) sed -n '1,32p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "error: 未知参数 $1（可用: --pkg-root --shell --platform --abi --check-only --list）" >&2; exit 2 ;;
  esac
done

command -v node >/dev/null 2>&1 || { echo "error: 需要 node（用来读子包 manifest.json）" >&2; exit 1; }

sha256_of() { # sha256_of <file>
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    certutil -hashfile "$(cygpath -w "$1" 2>/dev/null || echo "$1")" SHA256 | sed -n 2p | tr -d ' \r'
  fi
}

# 子包 dirname ← 平台 + ABI（与 build-npm-mobile.sh 的 subpkg_dirname 同一张表）
subpkg_dirname() { # subpkg_dirname <platform> <abi>  → 打印 dirname，未知返回 1
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

# 落盘目标目录（相对壳工程根）——与三份壳工程 .gitignore 忽略的路径一致。
target_dir() { # target_dir <platform> <abi>
  case "$1" in
    android) echo "$SHELL_DIR/android/app/src/main/jniLibs/$2" ;;
    harmony) echo "$SHELL_DIR/harmony/entry/libs/$2" ;;
    ios)     echo "$SHELL_DIR/ios/libs/$2" ;;
    *) echo "" ;;
  esac
}

# 找到一个已安装子包的目录：--pkg-root 优先，其次 node 解析，再次 $ROOT/node_modules。
# 一律打印**绝对路径**（后面用 node require 读 package.json/manifest.json，相对路径会被
# node 当成模块名解析而失败）。
resolve_dir() { # resolve_dir <dirname>  → 打印绝对路径，找不到返回 1
  if [ -n "$PKG_ROOT" ]; then
    [ -d "$PKG_ROOT/$1" ] && { (cd "$PKG_ROOT/$1" && pwd); return 0; }
    return 1
  fi
  local p
  p=$(node -e "console.log(require('path').dirname(require.resolve('@goxjs/$1/package.json')))" 2>/dev/null || true)
  if [ -n "$p" ] && [ -d "$p" ]; then echo "$p"; return 0; fi
  [ -d "$ROOT/node_modules/@goxjs/$1" ] && { (cd "$ROOT/node_modules/@goxjs/$1" && pwd); return 0; }
  return 1
}

# 引擎版本（仓库内才有；壳工程在仓库外时读不到，退化为只对子包内自洽）
ENGINE_VER=""
if [ -f "$ROOT/cmd/gox/main.go" ]; then
  ENGINE_VER=$(sed -n 's/.*const version *= *"\([^"]*\)".*/\1/p' "$ROOT/cmd/gox/main.go" | head -1)
fi
[ -n "$ENGINE_VER" ] && echo "==> 引擎版本  $ENGINE_VER"

want_platform() { # want_platform <platform>
  [ "$PLATFORM_ARG" = "all" ] && return 0
  case ",$PLATFORM_ARG," in *",$1,"*) return 0 ;; *) return 1 ;; esac
}

# 请求表：platform abi（ios 固定两个 SDK 切片；android/harmony 用 --abi）
REQUESTS=()
want_platform android && REQUESTS+=("android $ABI")
want_platform harmony && REQUESTS+=("harmony $ABI")
if want_platform ios; then
  REQUESTS+=("ios iphoneos-arm64")
  REQUESTS+=("ios iphonesimulator-arm64")
fi

matched=0
missed=""
checked_dirs=()

for req in "${REQUESTS[@]}"; do
  platform=${req%% *}; abi=${req#* }
  dirname=$(subpkg_dirname "$platform" "$abi") || { echo "error: 未知组合 $platform/$abi" >&2; exit 2; }

  if ! dir=$(resolve_dir "$dirname"); then
    missed="$missed ${dirname}"
    continue
  fi

  manifest="$dir/manifest.json"
  if [ ! -f "$manifest" ]; then
    echo "error: 子包 $dirname 里没有 manifest.json（装坏了？）" >&2
    exit 1
  fi

  # 子包身份：package.json 的 name 必须与目录名对得上
  pkgname=$(node -e "console.log(require(process.argv[1]).name)" "$dir/package.json")
  if [ "$pkgname" != "@goxjs/$dirname" ]; then
    echo "error: $dir 的 package.json name=$pkgname 与目录名 @goxjs/$dirname 不符" >&2
    exit 1
  fi

  manifest_ver=$(node -e "console.log(require(process.argv[1]).goxVersion)" "$manifest")
  # 校验 1：子包版本 == 引擎版本
  if [ -n "$ENGINE_VER" ] && [ "$manifest_ver" != "$ENGINE_VER" ]; then
    echo "error: 子包 $dirname 是版本 $manifest_ver，本仓库引擎是 $ENGINE_VER。" >&2
    echo "       壳工程与引擎必须成对发布：请装与引擎同版本的子包，或用 build-npm-mobile.sh" >&2
    echo "       从当前源码重编。错配后果是装到设备上才崩（JNI/NAPI 契约）。" >&2
    exit 1
  fi

  dst_dir=$(target_dir "$platform" "$abi")
  [ -n "$dst_dir" ] || { echo "error: 未知平台 $platform" >&2; exit 1; }

  # 校验 2：逐产物比对 sha256
  while IFS=$'\t' read -r rel kind sha size; do
    [ -n "$rel" ] || continue
    src="$dir/$rel"
    [ -f "$src" ] || { echo "error: $dirname 清单列了 $rel，但包里没有（半包？）" >&2; exit 1; }
    actual=$(sha256_of "$src")
    if [ "$actual" != "$sha" ]; then
      echo "error: $dirname/$rel 的 sha256 不符" >&2
      echo "       清单: $sha" >&2
      echo "       实际: $actual" >&2
      exit 1
    fi

    if [ "$LIST_ONLY" = 1 ]; then
      printf '  %-46s %-10s %8s B\n' "@goxjs/$dirname/$rel" "$kind" "$size"
    elif [ "$CHECK_ONLY" = 1 ]; then
      printf '  [ok] %-42s %-10s %8s B\n' "@goxjs/$dirname/$rel" "$kind" "$size"
    else
      mkdir -p "$dst_dir"
      cp "$src" "$dst_dir/"
      printf '  %-40s -> %s\n' "@goxjs/$dirname/$rel" "${dst_dir#"$ROOT"/}"
    fi
    matched=$((matched + 1))
  done < <(node -e '
const m = require(process.argv[1]);
for (const a of m.artifacts) console.log([a.path, a.kind, a.sha256, a.size].join("\t"));
' "$manifest")

  checked_dirs+=("$dirname@$manifest_ver")
done

if [ "$LIST_ONLY" = 1 ]; then
  echo
  echo "==> 解析到的子包：${checked_dirs[*]:-（无）}"
  [ "$matched" -gt 0 ] || { echo "error: 没有解析到任何子包（先 npm i @goxjs/goxjs-mobile-<platform>-<abi>）" >&2; exit 1; }
  exit 0
fi

# 校验 3：显式指定的 platform 必须有对应子包
if [ -n "$missed" ] && [ "$PLATFORM_ARG" != "all" ]; then
  echo "error: 以下子包未安装：$missed" >&2
  echo "       安装示例：npm i @goxjs/goxjs-mobile-android-arm64-v8a" >&2
  exit 1
fi
if [ "$matched" -eq 0 ]; then
  echo "error: 没有解析到任何子包（先 npm i @goxjs/goxjs-mobile-<platform>-<abi>）" >&2
  exit 1
fi

echo
if [ "$CHECK_ONLY" = 1 ]; then
  echo "==> 校验通过（--check-only，未落盘）：$matched 个产物，子包 $([ ${#checked_dirs[@]} -gt 0 ] && echo "${checked_dirs[*]}")"
else
  echo "==> 已放入壳工程：$matched 个产物，子包 ${checked_dirs[*]}"
fi
