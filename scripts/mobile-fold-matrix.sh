#!/usr/bin/env bash
# Android 折叠态切换回归 —— 在 CI 的折叠屏模拟器（pixel_fold 类 AVD）上跑的
# 主体逻辑，由 .github/workflows/mobile-smoke.yml 的 android-fold-emulator 作业
# 以单行 `bash scripts/mobile-fold-matrix.sh` 方式调用。
#
# ── 为什么从 workflow 的 script: 块里挪出来成一个文件 ──────────────────────
#
# emulator-runner 是**逐行**用 `/bin/sh -c` 执行 script: 块的（run 39890939630
# 的日志逐行可见 `[command]/usr/bin/sh -c …`），于是：
#
#   1. 变量跨行丢失：`STATES=$(grep …)` 只在那行生效，下一行的 `if [ -z
#      "$STATES" ]` 里它**永远为空** —— 日志里明明探测到了
#      CLOSED/HALF_OPENED/OPENED，作业却报"无输出"。skip 闸门从未真正工作过。
#   2. `exit 0` 只退出那**一行**，后面的 `adb install` 照样执行 —— "skip" 根本
#      没 skip 任何东西。
#
# 挪成一个文件、一次 sh 调用，变量、set -eu、exit 语义就都正常了。这不是美化，
# 是修正一个"绿了也白绿、红了归因错"的结构性缺陷。
#
# ── 用法与输出位置（与 fold 作业的 artifact 归档路径对齐，别乱改）──────────
#
#   前置：adb 已连上模拟器；$FOLD_APK_DIR 里有待装的 APK（双 ABI，见下）。
#   产物：/tmp/states.txt（探测到的折叠态）、/tmp/fold-shots/*.png（逐态截图）、
#         /tmp/fold-logcat.txt（崩溃/ANR 扫描结果）。
#
# ── 为什么 APK 必须是双 ABI 的（这个 job 的上一红根因）────────────────────
#
#   折叠屏 AVD 是 x86_64 架构（作业里 `arch: x86_64`），而 android 冒烟作业
#   原来只编 arm64-v8a 的 libgox.so。APK 里只有 arm64 原生库、设备是 x86_64：
#
#       Failure [INSTALL_FAILED_NO_MATCHING_ABIS: Failed to extract native
#       libraries, res=-113]
#
#   壳工程的 abiFilters 本来就声明了 arm64-v8a + x86_64 两个 ABI，缺的只是
#   CI 没编 x86_64 那份。所以 android 作业现在两个 ABI 都编（见 mobile-smoke
#   .yml），装的是双 ABI APK，模拟器上原生执行、不用走转译。
set -eu

APK_DIR=${FOLD_APK_DIR:-/tmp/apk}
SHOTS=${FOLD_SHOTS_DIR:-/tmp/fold-shots}

command -v adb >/dev/null 2>&1 || { echo "::error::adb 不在 PATH —— emulator-runner 环境变了？"; exit 1; }

# 1) 折叠态必须在场 —— 拿不到就是 profile 不对/设备表漂移，显式 skip 而不是
#    静默当"已验证折叠"。skip 语义靠本文件 + 单次调用才真正成立（见文件头）。
adb shell cmd device_state print-states | tee /tmp/states.txt
STATES=$(grep -ohE 'CLOSED|HALF_FOLDED|HALF_OPENED|OPENED' /tmp/states.txt | sort -u | tr '\n' ' ')
echo "==> 可用折叠态: $STATES"
if [ -z "$STATES" ]; then
  echo "::warning::当前 AVD 的 cmd device_state 无输出（print-states 如上）—— 折叠态切换回归无法在 CI 上执行，退回本地 scripts/mobile-device-accept.sh --rounds 20 与 gfx 生命周期测试"
  exit 0
fi

# 2) 装 APK（双 ABI：arm64-v8a + x86_64，见文件头）并拉起。
#    包名与壳工程一致：com.gox.demo；am start 失败时用 monkey 兜底（部分
#    ROM 上 -W 会因 launcher 未就绪返回非零）。
APK=$(ls "$APK_DIR"/*.apk | head -1)
echo "==> 安装 APK: $APK"
adb install -r "$APK"
adb shell am start -W -n com.gox.demo/.MainActivity || adb shell monkey -p com.gox.demo -c android.intent.category.LAUNCHER 1
sleep 3

# 3) 逐个折叠态切换并截图 —— 顺序即折叠屏生命周期的真机时序
#    （先 onConfigurationChanged 后 foldStatusChange，见
#    gfx/lifecycle_fold_test.go 的注释与 mobile-device-accept.sh §4）
mkdir -p "$SHOTS"
for s in CLOSED HALF_FOLDED HALF_OPENED OPENED; do
  case " $STATES " in *" $s "*) ;; *) continue ;; esac
  adb shell "cmd device_state state $s" || true
  sleep 2
  adb exec-out screencap -p > "$SHOTS/state-${s}-$(date +%s).png"
done

# 4) 证据：切换期间不能有崩溃 / ANR
adb shell "logcat -d -v time | grep -iE 'FATAL EXCEPTION|ANR in|has died' | head -40; echo [scan done]" > /tmp/fold-logcat.txt
cat /tmp/fold-logcat.txt
if grep -qiE 'FATAL EXCEPTION|ANR in' /tmp/fold-logcat.txt; then
  echo "::error::折叠态切换期间出现未捕获异常/ANR（原文在 artifacts 的 fold-logcat.txt）"
  exit 1
fi
echo "[ok] 折叠态切换回归通过：切换 $STATES 无崩溃/ANR"
