#!/usr/bin/env bash
# 交叉编译 Gox 的鸿蒙侧 Go 库（libgox.so，buildmode=c-shared）。
#
# 产物落在 dist/harmony/<abi>/libgox.so（dist/ 已 gitignore），由 app/harmony
# 壳工程拷进 libs/。
#
# 用法：
#     bash scripts/build-harmony.sh                    # arm64（真机）
#     bash scripts/build-harmony.sh --abi x86_64       # 模拟器
#     bash scripts/build-harmony.sh --sdk "H:/DevEco Studio/sdk/default/openharmony/native"
#     DEVECO_SDK_HOME=... bash scripts/build-harmony.sh
#
# ── 为什么不用 GOOS=openharmony（2026-10-01 实测）─────────────────────────
#
#   $ GOOS=openharmony GOARCH=arm64 go build ./...
#   unsupported GOOS/GOARCH pair openharmony/arm64
#
# Go 1.26.2 官方发行版**不含 openharmony target** —— 网上能搜到的
# `GOOS=openharmony` 全部来自社区 fork（ohos_golang_go），跟着写会白费一个
# 下午。鸿蒙 NDK 的 sysroot 是 **musl**，所以可行路线是拿官方 linux target
# 顶上去：
#
#     GOOS=linux GOARCH=arm64 CGO_ENABLED=1 CC=<ohos clang> \
#       go build -buildmode=c-shared -o libgox.so ./gfx/harmony/libgox
#
# 产出的 AArch64 ELF 就是鸿蒙能 dlopen 的形状（e_machine=0xB7），与 NDK 自带
# 的 libace_napi.z.so / libhilog_ndk.z.so 同类。脚本尾部有 ELF 头校验兜底。
#
# ── 三条实测出来的硬性细节 ───────────────────────────────────────────────
#
#   1. **不能用 SDK 里的 `aarch64-unknown-linux-ohos-clang`**。它是 `#!/bin/sh`
#      脚本（Android NDK 的 `.cmd` 也是同理不能直接用），Windows 上既不给
#      `.cmd` 后缀、也不在 `PATHEXT` 里，go 起不来 —— 症状是
#      `exec: "aarch64-unknown-linux-ohos-clang": executable file not found`。
#      本脚本改成**自己生成一个 `.cmd` wrapper**，内联 SDK 的 sysroot/target
#      参数（内容与官方 wrapper 等价）。
#   2. CC 必须是**绝对路径 + Windows 形式的斜杠**（`H:/…`）。MSYS 的 `/h/…`
#      会被 go 直接拒绝：`CC environment variable is relative`。
#   3. 必须显式 `-D__MUSL__`（官方 wrapper 里也有）。sysroot 头文件的
#      `features.h` 等分支靠它选 musl 那套，漏了会在编译标准头时就报奇怪的错。
#
# 另：路径含空格（`DevEco Studio`）是常态，wrapper 里所有路径都加引号；
# wrapper 自身落在仓库内（`.workbuddy/tmp`），避免往 DevEco 安装目录写东西。
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

ABI=${ABI:-arm64}
OUT="dist/harmony/$ABI"

SDK=${DEVECO_SDK_HOME:-}
while [ $# -gt 0 ]; do
  case "$1" in
    --sdk) SDK=$2; shift 2 ;;
    --abi) ABI=$2; OUT="dist/harmony/$2"; shift 2 ;;
    *) echo "error: 未知参数 $1" >&2; exit 2 ;;
  esac
done

# ---- 读取项目级配置 gox.json（存在时）----
# 与 build-android.sh 一致：提取 appId/version/icon 导出给 CI 用，顺带校验
# 源图标存在；鸿蒙侧 bundleName 由 hvigor 从壳工程的 app.json5 读，两者取值
# 应保持同一 appId。
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

# ---- 探测 HarmonyOS SDK 的 native 目录 ----
# 优先用显式传入的；否则扫 DevEco Studio 的默认安装位置（Win/Mac）与常见盘符。
# 判定标准是**目录里真有 llvm/bin/clang.exe 与 sysroot/**，而不是"目录存在"
# —— 装了一半的 SDK 目录挺常见。
SDK_CANDIDATES=(
  "$SDK"
  "${DEVECO_SDK_HOME:-}"
  "${OHOS_SDK_HOME:-}"
  "H:/DevEco Studio/sdk/default/openharmony/native"
  "C:/Program Files/Huawei/DevEco Studio/sdk/default/openharmony/native"
  "/Applications/DevEco-Studio.app/Contents/sdk/default/openharmony/native"
)
SDK_FOUND=""
for cand in "${SDK_CANDIDATES[@]}"; do
  [ -n "$cand" ] || continue
  if [ -f "$cand/llvm/bin/clang.exe" ] && [ -d "$cand/sysroot/usr/lib" ]; then
    SDK_FOUND=$cand
    break
  fi
  if [ -f "$cand/llvm/bin/clang" ] && [ -d "$cand/sysroot/usr/lib" ]; then
    SDK_FOUND=$cand
    break
  fi
done

if [ -z "$SDK_FOUND" ]; then
  echo "error: 找不到 HarmonyOS SDK 的 native 目录。" >&2
  echo "       用 --sdk <path> 或设 DEVECO_SDK_HOME=<...>/sdk/default/openharmony/native。" >&2
  echo "       该目录应包含 llvm/bin/clang(.exe) 与 sysroot/。" >&2
  exit 1
fi
SDK=$SDK_FOUND

CLANG="$SDK/llvm/bin/clang.exe"
[ -f "$CLANG" ] || CLANG="$SDK/llvm/bin/clang"
SYSROOT="$SDK/sysroot"

# ---- ABI → (OHOS 三元组, GOARCH, ELF e_machine 字节, 人读的名字, sysroot 库目录, HAP 里的 abi 目录) ----
#
# **这几列必须一起给**：曾经只按 ABI 选了三元组而 GOARCH 写死 arm64，于是
# `--abi x86_64` 会"成功"编出一份 arm64 的库、放在 x86_64 目录里 —— 直到装进
# 模拟器才以 `Cannot dlopen: wrong ELF class` 炸掉，归因要多绕一大圈。
# ELF 那两列同理：校验必须知道"这一次应该看到哪种机器码"，否则换个 ABI 就误报。
#
# `LIBDIR` 与 `HAP_ABI` 是**两件事**，别合并：
#   LIBDIR  = sysroot 里的库目录名，即 LLVM 三元组 (aarch64-linux-ohos)；
#   HAP_ABI = 壳工程 entry/libs/ 下的子目录名，是 Android 那套 (arm64-v8a)。
# 混用的症状是 .so 放进了 hvigor 不认识的目录，构建绿、运行时 dlopen 失败。
case "$ABI" in
  arm64|arm64-v8a) TRIPLE=aarch64-linux-ohos  GOARCH=arm64  LIBDIR=aarch64-linux-ohos  HAP_ABI=arm64-v8a   MACHINE="03 00 b7 00" MACHINE_NAME="AArch64" ;;
  x86_64)          TRIPLE=x86_64-linux-ohos   GOARCH=amd64  LIBDIR=x86_64-linux-ohos   HAP_ABI=x86_64      MACHINE="03 00 3e 00" MACHINE_NAME="x86-64"  ;;
  armeabi-v7a|arm) TRIPLE=arm-linux-ohos      GOARCH=arm    LIBDIR=arm-linux-ohos      HAP_ABI=armeabi-v7a MACHINE="03 00 28 00" MACHINE_NAME="ARM"     ;;
  *) echo "error: 不支持的 ABI $ABI（可选 arm64 / x86_64 / armeabi-v7a）" >&2; exit 2 ;;
esac

[ -d "$SYSROOT/usr/lib/$LIBDIR" ] || {
  echo "error: sysroot 里没有 $LIBDIR 的库目录（SDK 可能未装齐该 ABI）" >&2
  exit 1
}

command -v go >/dev/null 2>&1 || { echo "error: go not found in PATH" >&2; exit 1; }

# ---- 生成 CC wrapper ----
#
# 为什么不能直接把 SDK 里的 clang 交给 go：它是 `#!/bin/sh` 脚本（Android NDK
# 的 `.cmd` 同理），Windows 上既不给 `.cmd` 后缀、也不在 `PATHEXT` 里，go 起不来
# —— 症状是 `exec: "...": executable file not found`。所以在仓库内落一个等价的
# wrapper（内容 = 官方那几行参数）。
#
# **两种形态，按宿主分**（2026-10-22 补 Linux 分支）：
#
#   Windows（有 cygpath）→ `.cmd`：go.exe 是 Windows 原生程序，只认 .cmd，
#     且内容必须 CRLF + 纯 ASCII、路径转 Windows 形式（`H:/…`）。
#   Linux / macOS        → `.sh` + chmod +x。
#
# **以前这里无条件生成 .cmd**，于是在 Linux 上 go 拿到的是一段 `@echo off` 文本
# —— 既没有 shebang 也不是可执行脚本，直接执行失败（脚本事实上只支持 Windows）。
# 下面这个分支就是修它。
#
# 另：写文件用 MSYS 路径、交给 go 的路径用 Windows 形式，是**两件事** —— 重定向
# 目标写 Windows 形式会被当成相对路径落到奇怪的地方。
mkdir -p .workbuddy/tmp

CLANG_FOR_CC=$CLANG
SYSROOT_FOR_CC=$SYSROOT
if command -v cygpath >/dev/null 2>&1; then
  CLANG_FOR_CC=$(cygpath -m "$CLANG")
  SYSROOT_FOR_CC=$(cygpath -m "$SYSROOT")
  CC_CMD="$(cygpath -m "$ROOT")/.workbuddy/tmp/cc-ohos-$ABI.cmd"
  # 路径含空格（“DevEco Studio”）是常态 ⇒ 全部加引号。
  printf '@echo off\r\n"%s" -target %s --sysroot="%s" -D__MUSL__ %%*\r\n' \
    "$CLANG_FOR_CC" "$TRIPLE" "$SYSROOT_FOR_CC" > "$(cygpath -u "$CC_CMD")"
else
  CC_CMD="$ROOT/.workbuddy/tmp/cc-ohos-$ABI.sh"
  printf '#!/bin/sh\nexec "%s" -target %s --sysroot="%s" -D__MUSL__ "$@"\n' \
    "$CLANG" "$TRIPLE" "$SYSROOT" > "$CC_CMD"
  chmod +x "$CC_CMD"
fi

mkdir -p "$OUT"
echo "==> SDK     $SDK"
echo "==> TRIPLE  $TRIPLE (GOARCH=$GOARCH)"
echo "==> SYSROOT $SYSROOT"
echo "==> CC      $CC_CMD"

# ---- 编译 ----
#
# **`-tags harmony` 不能省**（2026-10-01 实测踩到）。`GOOS=linux` 不会带上任何
# `harmony` 约束 —— gfx/harmony 下每个文件都写着 `//go:build harmony`，不显式开
# 这个 tag 时它们**全部被排除**，go 只报一句：
#
#     package .../gfx/harmony/libgox: build constraints exclude all Go files
#
# 这句话很容易被误读成"目录里没有 .go 文件"（其实文件都在）。鸿蒙没有平台级
# GOOS 可用，所以这个 tag 就是事实上的平台标记，由构建脚本负责开。
#
# 鸿蒙的 -l 名字带 `.z` 后缀（libace_napi.z.so）是官方约定，不是笔误。
GOOS=linux GOARCH="$GOARCH" CGO_ENABLED=1 CC="$CC_CMD" \
  go build -tags harmony -buildmode=c-shared -o "$OUT/libgox.so" ./gfx/harmony/libgox

# ---- 产出校验 ----
# 交叉编译"成功"但链错目标这种事，只看退出码是发现不了的。
# ELF 头前 20 字节：e_type=03 00 (DYN) + e_machine。
if command -v od >/dev/null 2>&1; then
  HDR=$(od -An -tx1 -N20 "$OUT/libgox.so" | tr -s ' \n' ' ')
  case "$HDR" in
    *"$MACHINE"*) echo "[ok] ELF 校验通过：$MACHINE_NAME 共享库" ;;
    *)
      echo "::error::libgox.so 不是 $MACHINE_NAME 共享库（ABI=$ABI），ELF 头：$HDR" >&2
      echo "       期望的 e_type+e_machine 字节：$MACHINE" >&2
      exit 1
      ;;
  esac
fi

# 动态依赖必须是 musl libc，用它区分"链到了开发机 glibc"这种静默错编。
if command -v strings >/dev/null 2>&1; then
  NEEDED=$(strings "$OUT/libgox.so" | grep -E '^lib[a-z0-9_+.-]*\.so(\.[0-9]+)*$' | sort -u | tr '\n' ' ')
  echo "==> NEEDED: $NEEDED"
fi

ls -lh "$OUT/libgox.so"
echo "完成：把 $OUT/libgox.so 拷进壳工程 app/harmony/entry/libs/$HAP_ABI/"
