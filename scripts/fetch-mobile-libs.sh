#!/usr/bin/env bash
# 从已安装的 @goxjs/goxjs 包里取出预编译的移动端库，校验后拷进三平台壳工程。
#
# 与 build-npm-mobile.sh 是一出一进的一对：
#   build-npm-mobile.sh  → 编 libgox 并把产物 staging 进 npm/mobile/   （发布端）
#   fetch-mobile-libs.sh → 从装好的 @goxjs/goxjs/mobile/ 取出产物进壳工程（消费端）
#
# 为什么要有这个「消费端」（M11）：移动端分发面把预编译库发进 npm 之后，壳工程得
# 真能拿到它，否则发出去的 mobile/ 只是死重量 —— 用户仍要克隆 Gox 仓库、装 NDK/
# Xcode 从源码编。有了这一步，壳工程只依赖一个 npm 包即可拿到 libgox。
#
# 校验三件事（都属于"运行时才崩、编译期拦不住"的错，所以在这里挡住）：
#   1. manifest.json 的 goxVersion 必须等于引擎版本（cmd/gox/main.go 的 const version）
#      —— 防「新壳配旧引擎」：JNI/NAPI 契约不匹配，是装到设备上才炸的；
#   2. 每个产物的 sha256 必须与 manifest 记的一致 —— 防半包 / 被截断 / 被换；
#   3. 目标 platform+abi 必须真的出现在 manifest 里 —— 防"以为装上了，其实没有"。
#
# 用法：
#   bash scripts/fetch-mobile-libs.sh                    # 自动找 node_modules 里的 @goxjs/goxjs，三平台 arm64
#   bash scripts/fetch-mobile-libs.sh --platform android --abi arm64-v8a
#   bash scripts/fetch-mobile-libs.sh --pkg <包目录>      # 显式指定 @goxjs/goxjs 包目录（内含 mobile/）
#   bash scripts/fetch-mobile-libs.sh --shell <壳工程根>  # 默认 app/；拷到别处便于测试
#   bash scripts/fetch-mobile-libs.sh --check-only       # 只做三件校验与 sha 比对，不落盘
#
# 退出码：0 = 请求的产物全部校验通过并拷好；1 = 校验失败或找不到包/产物。
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

PKG=""
SHELL_DIR="$ROOT/app"
PLATFORM_ARG="all"
ABI="arm64-v8a"
CHECK_ONLY=0

while [ $# -gt 0 ]; do
  case "$1" in
    --pkg) PKG=$2; shift 2 ;;
    --shell) SHELL_DIR=$2; shift 2 ;;
    --platform) PLATFORM_ARG=$2; shift 2 ;;
    --abi) ABI=$2; shift 2 ;;
    --check-only) CHECK_ONLY=1; shift ;;
    -h|--help) sed -n '1,27p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "error: 未知参数 $1（可用: --pkg --shell --platform --abi --check-only）" >&2; exit 2 ;;
  esac
done

command -v node >/dev/null 2>&1 || { echo "error: 需要 node（用来读 manifest.json）" >&2; exit 1; }

# ---- 定位 @goxjs/goxjs 包目录 ----
# 优先让 node 解析（尊重 node_modules 提升 / npm workspaces），再退到常见位置；
# 仓库内自测时 npm/ 子模块本身就是一个包目录。
if [ -z "$PKG" ]; then
  resolved=$(node -e "console.log(require('path').dirname(require.resolve('@goxjs/goxjs/package.json')))" 2>/dev/null || true)
  if [ -n "$resolved" ] && [ -d "$resolved" ]; then
    PKG="$resolved"
  elif [ -d "$ROOT/node_modules/@goxjs/goxjs" ]; then
    PKG="$ROOT/node_modules/@goxjs/goxjs"
  elif [ -f "$ROOT/npm/mobile/manifest.json" ]; then
    PKG="$ROOT/npm"      # 仓库内自测：用 npm/ 子模块当包目录
  else
    echo "error: 找不到 @goxjs/goxjs 包（先 npm i @goxjs/goxjs，或用 --pkg 指定）" >&2
    exit 1
  fi
fi
[ -d "$PKG" ] || { echo "error: 包目录不存在: $PKG" >&2; exit 1; }
[ -f "$PKG/package.json" ] || { echo "error: $PKG 里没有 package.json" >&2; exit 1; }

MANIFEST="$PKG/mobile/manifest.json"
if [ ! -f "$MANIFEST" ]; then
  echo "error: $PKG 里没有 mobile/manifest.json —— 这个版本没有带移动端产物。" >&2
  echo "       需要 @goxjs/goxjs ≥ 带 mobile/ 的版本（M11）；发布端用 build-npm-mobile.sh 产出。" >&2
  exit 1
fi

sha256_of() { # sha256_of <file>
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    certutil -hashfile "$(cygpath -w "$1" 2>/dev/null || echo "$1")" SHA256 | sed -n 2p | tr -d ' \r'
  fi
}

PKG_VER=$(node -e "console.log(require(process.argv[1]).version)" "$PKG/package.json")
MANIFEST_VER=$(node -e "console.log(require(process.argv[1]).goxVersion)" "$MANIFEST")

# 引擎版本（仓库内才有；壳工程在仓库外时读不到，退化为只对包内自洽）
ENGINE_VER=""
if [ -f "$ROOT/cmd/gox/main.go" ]; then
  ENGINE_VER=$(sed -n 's/.*const version *= *"\([^"]*\)".*/\1/p' "$ROOT/cmd/gox/main.go" | head -1)
fi

echo "==> 包        $PKG (@goxjs/goxjs@$PKG_VER)"
echo "==> 清单      $MANIFEST (goxVersion=$MANIFEST_VER)"
[ -n "$ENGINE_VER" ] && echo "==> 引擎版本  $ENGINE_VER"
echo "==> 壳工程    $SHELL_DIR"
echo "==> 平台      $PLATFORM_ARG (abi=$ABI)"

# ---- 校验 1：清单版本 == 包版本 == 引擎版本 ----
if [ "$MANIFEST_VER" != "$PKG_VER" ]; then
  echo "error: manifest.goxVersion=$MANIFEST_VER 与包版本 $PKG_VER 不一致（包被改坏？）" >&2
  exit 1
fi
if [ -n "$ENGINE_VER" ] && [ "$MANIFEST_VER" != "$ENGINE_VER" ]; then
  echo "error: 预编译库版本 $MANIFEST_VER ≠ 本仓库引擎版本 $ENGINE_VER。" >&2
  echo "       壳工程与引擎必须成对发布：请装与引擎同版本的 @goxjs/goxjs，" >&2
  echo "       或用 build-npm-mobile.sh 从当前源码重编。错配后果是装到设备上才崩（JNI/NAPI 契约）。" >&2
  exit 1
fi

# ---- 把 manifest 的 artifacts 拉成 TSV：path platform abi kind sha256 size ----
ARTIFACTS=$(node -e '
const m = require(process.argv[1]);
for (const a of m.artifacts) {
  console.log([a.path, a.platform, a.abi, a.kind, a.sha256, a.size].join("\t"));
}
' "$MANIFEST")

want_platform() { # want_platform <platform>
  [ "$PLATFORM_ARG" = "all" ] && return 0
  case ",$PLATFORM_ARG," in *",$1,"*) return 0 ;; *) return 1 ;; esac
}

# 每个平台的落盘目标目录（相对壳工程根）——与三份壳工程 .gitignore 忽略的路径一致。
target_dir() { # target_dir <platform> <abi>
  case "$1" in
    android) echo "$SHELL_DIR/android/app/src/main/jniLibs/$2" ;;
    harmony) echo "$SHELL_DIR/harmony/entry/libs/$2" ;;
    ios)     echo "$SHELL_DIR/ios/libs/$2" ;;
    *) echo "" ;;
  esac
}

matched=0
missed=""
while IFS=$'\t' read -r path platform abi kind sha size; do
  [ -n "$path" ] || continue
  want_platform "$platform" || continue
  # android / harmony 按 --abi 过滤；ios 的 abi 是 <sdk>-<arch>，全收
  if [ "$platform" != "ios" ] && [ "$abi" != "$ABI" ]; then continue; fi

  src="$PKG/$path"
  if [ ! -f "$src" ]; then
    echo "error: 清单列了 $path，但包里没有这个文件（半包？）" >&2
    exit 1
  fi
  actual=$(sha256_of "$src")
  if [ "$actual" != "$sha" ]; then
    echo "error: $path 的 sha256 不符" >&2
    echo "       清单: $sha" >&2
    echo "       实际: $actual" >&2
    exit 1
  fi

  dst_dir=$(target_dir "$platform" "$abi")
  [ -n "$dst_dir" ] || { echo "error: 未知平台 $platform" >&2; exit 1; }
  if [ "$CHECK_ONLY" = 1 ]; then
    printf '  [ok] %-52s %8s B  (%s)\n' "$path" "$size" "$kind"
  else
    mkdir -p "$dst_dir"
    cp "$src" "$dst_dir/"
    printf '  %-52s -> %s\n' "$path" "${dst_dir#"$ROOT"/}"
  fi
  matched=$((matched + 1))
done <<< "$ARTIFACTS"

# ---- 校验 3：请求的 platform+abi 是否真的都有 ----
# 显式 --platform 才逐个卡（编不出就算失败，免得"以为装上了"）；缺省 all 只要求
# 至少命中一个（各发布环境能产出的平台本就不同：harmony 要 DevEco、ios 要 macOS）。
if [ "$PLATFORM_ARG" != "all" ]; then
  for p in $(echo "$PLATFORM_ARG" | tr ',' ' '); do
    case "$p" in
      ios) continue ;;  # ios 不按 abi 过滤，有就收了
      android|harmony)
        if ! printf '%s\n' "$ARTIFACTS" | awk -F'\t' -v p="$p" -v a="$ABI" '$2==p && $3==a{f=1} END{exit !f}'; then
          missed="$missed $p/$ABI"
        fi
        ;;
    esac
  done
fi

if [ "$matched" -eq 0 ]; then
  echo "error: 清单里没有匹配 $PLATFORM_ARG/$ABI 的产物" >&2
  exit 1
fi
if [ -n "$missed" ]; then
  echo "error: 清单里缺少请求的产物:$missed" >&2
  echo "       该版本的 @goxjs/goxjs 没有为这些目标构建；用 build-npm-mobile.sh 补齐后再发布。" >&2
  exit 1
fi

echo
if [ "$CHECK_ONLY" = 1 ]; then
  echo "==> 校验通过（--check-only，未落盘）：$matched 个产物"
else
  echo "==> 已放入壳工程：$matched 个产物（版本 $MANIFEST_VER）"
fi
