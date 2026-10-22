#!/usr/bin/env bash
# 鸿蒙侧 Go 代码的**桌面类型检查**（无 OHOS SDK 也能跑）。
#
# 用法：
#     bash scripts/check-harmony.sh
#
# ── 为什么有这个脚本 ──────────────────────────────────────────────────────
#
# 鸿蒙是三端里**唯一零 CI 覆盖**的一端：Android 有 APK 打包，iOS 有模拟器编译，
# 鸿蒙因为托管 runner 上装不了 DevEco SDK（华为镜像只有设备 L2 SDK，hf-mirror
# 拉不通），改动全靠"本地有 Windows + DevEco 的人手工编一次"。结果就是**改
# gfx/harmony 下的 Go 代码在 CI 上完全没有反馈**，错了只能等真机。
#
# 但"编不了"和"查不了"是两件事。鸿蒙 Go 侧的 C 依赖其实只有两块：
#   - `node_api.h` —— 34 个 napi_* 全是 **Node-API 标准符号**，与 OHOS 无关；
#   - `hilog/log.h` —— 只用了 OH_LOG_Print / LogType / LogLevel 几个符号。
# 所以**拿真 Node 的 node_api.h + 一个 hilog 替身头文件**，就能在任意 Linux
# 机器上把鸿蒙侧 Go 代码完整编译一遍（含全部 cgo 的 C 代码），不用 OHOS SDK。
#
# ── 检查到什么程度（边界写清楚，别当真机构建用）────────────────────────────
#
# 真能拦住的：
#   1. Go 侧编译错误（类型、未定义标识符、构建约束）；
#   2. cgo 的 **C 代码真的过 gcc** —— napi_* 参数类型/个数写错、结构体字段名
#      写错，都会在 C 编译阶段报错；
#   3. napi_* 符号写错名字（比如写成不存在的 `napi_get_value_string`）——
#      靠 `-Werror=implicit-function-declaration`：头文件里没有的函数被 GCC 当
#      "隐式声明"，直接升级为硬错误。**没有这个 flag 时 GCC 只警告**，等于漏检。
#   4. 完整的 `-buildmode=c-shared` 链路（与 scripts/build-harmony.sh 的 go
#      命令形状一致，只是目标换成宿主）。
#
# 拦不住的（如实列出）：
#   - **链接期**符号缺失：`-lace_napi.z` / `-lhilog_ndk.z` 是 OHOS 的库名，
#     这里用两个空壳 .so 顶上（见下），且 Linux 链 .so 默认允许未定义符号，
#     所以"头文件里有、OHOS 的 libace_napi.z.so 里没有"这类差异查不出来；
#   - 任何**运行时**行为；
#   - OHOS 的 napi 与 Node 的 napi 若有**签名差异**，此处以 Node 头文件为准。
#
# 所以它的定位是「**至少让鸿蒙改动有编译反馈**」，不是「替代真机验收」。
#
# ══ 产物警告 ══
# 产出的 .so 是**宿主架构（x86-64）的 ELF**，与鸿蒙设备无关，**严禁**拷进
# app/harmony/entry/libs/。脚本末尾会断言它的 ELF 机器码**不是** aarch64，
# 撞上就报错——防止有人误把它当真产物用。
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

INC="$ROOT/scripts/harmony-typecheck/include"
OUT="dist/harmony-typecheck"

[ -f "$INC/node_api.h" ] || { echo "error: 缺 $INC/node_api.h" >&2; exit 1; }
[ -f "$INC/hilog/log.h" ] || { echo "error: 缺 $INC/hilog/log.h" >&2; exit 1; }
command -v go >/dev/null 2>&1 || { echo "error: go not found in PATH" >&2; exit 1; }
command -v cc >/dev/null 2>&1 || { echo "error: C 编译器 (cc) not found in PATH" >&2; exit 1; }

mkdir -p "$OUT"

# ---- 两个空壳 .so，只为满足 #cgo LDFLAGS 里的 -lace_napi.z / -lhilog_ndk.z ----
#
# gfx/harmony/harmony.go 里写死了 `#cgo LDFLAGS: -lace_napi.z -lhilog_ndk.z`
# （鸿蒙的库名确实带 `.z` 后缀）。桌面上没有这两个库，链接必然失败：
#
#     /usr/bin/ld: cannot find -lace_napi.z
#
# 造两个**只含一个符号**的同名 .so 顶上。注意这里**不**放 napi_* 的实现：
# 放了反而更假（等于自己给自己发合格证）。留空 = 如实承认"链接这层没验"。
# 必须是**绝对路径**：go 的链接步骤不在仓库根目录跑，相对 -L 会指到别处，
# 症状还是 `cannot find -lace_napi.z`（2026-10-22 实测踩到）。
STUBDIR="$ROOT/$OUT/stublibs"
mkdir -p "$STUBDIR"
cat > "$STUBDIR/stub.c" <<'EOF'
/* 空壳：只为让链接器找到 -lace_napi.z / -lhilog_ndk.z 这两个 OHOS 库名。
   不含任何 napi_* 实现 —— 本脚本只做编译期类型检查，不假装验到链接层。 */
int gox_harmony_typecheck_stub = 0;
EOF
for lib in ace_napi.z hilog_ndk.z; do
  cc -shared -fPIC -o "$STUBDIR/lib$lib.so" "$STUBDIR/stub.c"
done

export CGO_ENABLED=1
# ── 这几个 -Werror= 是**本检查的命门**，一个个都是实测试出来的 ──────────
#
# cgo 只在"探测"阶段用 -Wall -Werror，**真正编译 C 时用的是这里的 CGO_CFLAGS**。
# 结果就是：不显式升错误的话，下面这些全是**警告**，脚本照样 exit 0，闸门形同
# 虚设。2026-10-22 逐个做过负向对照（改一行 → 必须变红），清单如下：
#
#   implicit-function-declaration  写错 napi 函数名（头文件里没有）→ 必须红
#   incompatible-pointer-types     该传 napi_value* 传成了 napi_value → 必须红
#   int-conversion                 指针/整数互转写错 → 必须红
#   return-type                    C 函数漏 return → 必须红
#   discarded-qualifiers           const char* 丢 const → 必须红
#
# 故意**不**写 -Wall -Werror：那会把 cgo 生成代码里的噪音也升成错误，不同 GCC
# 版本还不一样，属于自找麻烦。只升这五条"真的会咬人"的。
export CGO_CFLAGS="-I$INC -Werror=implicit-function-declaration -Werror=incompatible-pointer-types -Werror=int-conversion -Werror=return-type -Werror=discarded-qualifiers"
export CGO_LDFLAGS="-L$STUBDIR"

echo "==> 步骤 1/2: go vet（鸿蒙 tag）"
# -unsafeptr=false：gfx/harmony/libgox/main.go 里有一处
# `unsafe.Pointer(uintptr(idx))`（cgo 回调传参的惯用写法），是**既有**代码、
# 不是本次引入的回归；vet 的 unsafeptr 规则会命中它。这里关掉这一条规则，
# 其余 vet 检查照跑。注释留着，等哪天那行改掉了就可以摘掉这个 flag。
go vet -unsafeptr=false -tags harmony ./gfx/harmony/...

echo "==> 步骤 2/2: go build -buildmode=c-shared（鸿蒙 tag, 宿主架构）"
go build -tags harmony -buildmode=c-shared -o "$OUT/libgox.so" ./gfx/harmony/libgox

# ---- 反误用断言：产物必须是**宿主**架构，不能是鸿蒙设备用的 aarch64 ----
if command -v od >/dev/null 2>&1; then
  MACHINE=$(od -An -tx1 -j18 -N2 "$OUT/libgox.so" | tr -d ' \n')
  case "$MACHINE" in
    b700)
      echo "::error::产物是 AArch64 —— 这不是真机构建的产物，说明环境串了" >&2
      exit 1
      ;;
    *)
      echo "[ok] 产物机器码 0x$MACHINE（宿主架构，仅供检查，禁止用于鸿蒙设备）"
      ;;
  esac
fi

ls -lh "$OUT/libgox.so"
echo
echo "完成：鸿蒙侧 Go 代码在宿主上通过编译期类型检查。"
echo "      注意这是类型检查，**不是**真机构建 —— 产物不可用于鸿蒙设备。"
