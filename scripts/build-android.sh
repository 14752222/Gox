#!/usr/bin/env bash
# 交叉编译 Gox 的 Android 侧 Go 库（libgox.so，buildmode=c-shared）。
#
# 与 build-npm.sh 的关键差异：**这条路必须开 cgo**。移动端的窗口/输入/软键盘
# 只存在于 Java 侧，跨语言桥接绕不开 JNI —— 这是 2026-09-18 拍板路线 A 时对
# 「纯 Go、零 cgo」硬约束的正式修订（见 agent_doc/mobile-port-plan.md）。
#
# 产物落在 dist/android/<abi>/libgox.so（dist/ 已 gitignore），由 app/android
# 壳工程拷进 jniLibs/。
#
# 用法：
#     bash scripts/build-android.sh                 # arm64-v8a（真机）
#     bash scripts/build-android.sh --abi x86_64    # 模拟器（x86 主机上是原生执行，比转译 arm 快得多）
#     bash scripts/build-android.sh --ndk H:/AndroidSDK/ndk/28.2.13676358
#     ANDROID_NDK_HOME=... bash scripts/build-android.sh
#
# 三条实测出来的硬性细节（2026-09-22 探针验证，写在注释里免得后人再踩）：
#   1. CC 必须是**绝对路径**，且给原生 go.exe 时要 Windows 形式（H:/…）。
#      MSYS 的 /h/… 会被 go 直接拒绝：CC environment variable is relative。
#   2. 必须用**版本化的 wrapper**（aarch64-linux-android21-clang.cmd），不能用裸
#      clang.exe：裸 clang 不会把 --target/--sysroot 传给链接器，报
#      `lld: error: unknown argument: -z`。
#   3. 不需要 gomobile（也就免掉 golang.org/x/mobile 这个全新依赖）——
#      手写 JNI + c-shared 已经够用。
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

ABI=${ABI:-arm64-v8a}
OUT="dist/android/$ABI"

NDK=${ANDROID_NDK_HOME:-}
while [ $# -gt 0 ]; do
  case "$1" in
    --ndk) NDK=$2; shift 2 ;;
    --abi) ABI=$2; OUT="dist/android/$2"; shift 2 ;;
    *) echo "error: 未知参数 $1" >&2; exit 2 ;;
  esac
done

# ---- 读取项目级配置 gox.json（存在时）----
# 提取 appId/version/icon 导出到 GOX_APP_ID / GOX_VERSION / GOX_ICON,
# 供后续 gradle 注入或 CI 使用; 也顺带校验源图标存在（真机验收前发现问题）。
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

if [ -z "$NDK" ]; then
  for base in "${ANDROID_HOME:-}" "${ANDROID_SDK_ROOT:-}" "H:/AndroidSDK" "$HOME/Android/Sdk"; do
    [ -n "$base" ] && [ -d "$base/ndk" ] || continue
    # 多个 NDK 版本时取名字最大的那个（版本号按字典序恰好也是升序）
    cand=$(ls -1 "$base/ndk" | sort | tail -1)
    [ -n "$cand" ] && NDK="$base/ndk/$cand" && break
  done
fi

if [ -z "$NDK" ] || [ ! -d "$NDK" ]; then
  echo "error: 找不到 Android NDK。用 --ndk <path> 或设 ANDROID_NDK_HOME。" >&2
  exit 1
fi

# ABI → (NDK 三元组, GOARCH, ELF e_machine 字节, 人读的名字)
#
# **两列必须一起给**：曾经只按 ABI 选了编译器三元组而 GOARCH 写死 arm64，于是
# `--abi x86_64` 会"成功"编出一份 arm64 的库、放在 x86_64 目录里 —— 直到装进
# 模拟器才以 `UnsatisfiedLinkError: 找不到 native 方法` 炸掉，归因要多绕一大圈。
# ELF 那两列同理：校验必须知道"这一次应该看到哪种机器码"，否则换个 ABI 就误报。
case "$ABI" in
  arm64-v8a)   TRIPLE=aarch64-linux-android    GOARCH=arm64  MACHINE="03 00 b7 00" MACHINE_NAME="AArch64" ;;
  armeabi-v7a) TRIPLE=armv7a-linux-androideabi GOARCH=arm    MACHINE="03 00 28 00" MACHINE_NAME="ARM" ;;
  x86_64)      TRIPLE=x86_64-linux-android     GOARCH=amd64  MACHINE="03 00 3e 00" MACHINE_NAME="x86-64" ;;
  *) echo "error: 不支持的 ABI $ABI" >&2; exit 2 ;;
esac

# API 级别 21 是 Gox 的下限（Go 官方对 android 的最低支持），与 NDK wrapper 名一致。
TOOLCHAIN="$NDK/toolchains/llvm/prebuilt/windows-x86_64/bin"
[ -d "$TOOLCHAIN" ] || TOOLCHAIN="$NDK/toolchains/llvm/prebuilt/linux-x86_64/bin"
CC_PATH="$TOOLCHAIN/${TRIPLE}21-clang.cmd"
[ -f "$CC_PATH" ] || CC_PATH="$TOOLCHAIN/${TRIPLE}21-clang"

if [ ! -f "$CC_PATH" ]; then
  echo "error: 找不到编译器 wrapper: $CC_PATH" >&2
  echo "       （必须用版本化 wrapper，不能用裸 clang）" >&2
  exit 1
fi

# go.exe 是原生 Windows 程序：MSYS 的 /h/… 它不认，转成 H:/…（-m = 混用斜杠）
if command -v cygpath >/dev/null 2>&1; then
  CC_PATH=$(cygpath -m "$CC_PATH")
fi

command -v go >/dev/null 2>&1 || { echo "error: go not found in PATH" >&2; exit 1; }

mkdir -p "$OUT"
echo "==> NDK   $NDK"
echo "==> CC    $CC_PATH"
echo "==> ABI   $ABI (GOARCH=$GOARCH)"

GOOS=android GOARCH="$GOARCH" CGO_ENABLED=1 CC="$CC_PATH" \
  go build -buildmode=c-shared -o "$OUT/libgox.so" ./gfx/android/libgox

# 产出必须是这个 ABI 对应的共享库：交叉编译"成功"但链错目标这种事，只看退出码
# 是发现不了的（ELF 头前 20 字节：e_type=03 00 DYN + e_machine）。
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

ls -lh "$OUT/libgox.so"
echo "完成：把 $OUT/libgox.so 拷进壳工程的 src/main/jniLibs/$ABI/"
