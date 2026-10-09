#!/usr/bin/env bash
# 真机验收「插一次设备就跑完」的一键采集脚本（看板单 rpr9zf）。
#
# 为什么要有这个脚本：mobile-regression-checklist.md 里的真机项过去靠人记步骤，
# 一次插设备常常只验一半 —— 验到一半发现 density 没采，只能拔了重插。本脚本把
# §1（屏幕与密度）/ §2（触摸与多指手势）/ §3（帧率·内存·冷启动）需要的真机数据
# 在**一次插设备内**全部取完，落一份 JSON 存档，再打印人读摘要。
#
# 指标口径、单位与空基线表见 docs/mobile-perf-baseline.md。与 docs/performance.md
# 的分工：那边是**开发机**的解释器算力 / GC 毛刺基准，这边是**真机**上的
# 显示·触控·帧率·内存·启动指标，两者不可直接相减。
#
# 用法：
#   bash scripts/mobile-device-accept.sh                       # 一台在线设备，全量采集
#   bash scripts/mobile-device-accept.sh --device <serial>     # 多台在线时指定
#   bash scripts/mobile-device-accept.sh --package com.x.app   # 指定被测包（§3 需要）
#   bash scripts/mobile-device-accept.sh --activity .MainActivity
#   bash scripts/mobile-device-accept.sh --dry-run             # 无设备也能跑：只打印命令清单，退出 0
#
# 退出码：
#   0 = 采集完成（或 --dry-run）；
#   1 = 跑完了但有未完成项（JSON 仍落盘，缺项标 null，并打印「本次未完成项」）；
#   2 = 前置失败：adb 不在 PATH / 参数错误；
#   3 = 设备不可用：无在线设备、多台在线未指定、或设备未授权（offline/unauthorized）。
#
# 依赖只有 adb（Android platform-tools）。其余外部命令（getevent / timeout /
# dumpsys 子命令 / input motionevent…）在真机上缺失时**不中断脚本**：对应项标
# unavailable 并汇总进「本次未完成项」。因此本脚本**故意不用 set -e** —— 在真机上
# 缺一条命令就整体退出，比缺一项数据更难救（拔一次设备的成本远高于补一项）。
#
# 硬约束：**不造假数据**。采不到就是采不到，JSON 里写 null + 状态 unavailable，
# 人读摘要里如实标注，绝不用桌面数字或上一轮的旧值填充。
set -uo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

DRY_RUN=0
SERIAL=""
PKG=${PKG:-}
ACT=${ACT:-}
OUT_DIR="$ROOT/bench-results"
SAMPLE_SEC=${SAMPLE_SEC:-5}   # §3 帧率采样窗口（秒）：窗口内持续注入滑动再读 gfxinfo
MAX_OUT_LINES=60              # 单条原始输出最多存多少行（防 JSON 被 dumpsys 撑爆）

while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run)   DRY_RUN=1; shift ;;
    --device|-s) SERIAL=$2; shift 2 ;;
    --package)   PKG=$2; shift 2 ;;
    --activity)  ACT=$2; shift 2 ;;
    --out)       OUT_DIR=$2; shift 2 ;;
    --sample-sec) SAMPLE_SEC=$2; shift 2 ;;
    -h|--help) sed -n '1,40p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "error: 未知参数 $1（可用: --dry-run --device --package --activity --out --sample-sec）" >&2; exit 2 ;;
  esac
done

# ── 基础工具 ────────────────────────────────────────────────────────────────
have() { command -v "$1" >/dev/null 2>&1; }

# adb 调用统一走这里：显式 -s 时带 serial，否则让 adb 自己选（0/1 台设备时对）。
adbrun() {
  if [ -n "$SERIAL" ]; then adb -s "$SERIAL" "$@"; else adb "$@"; fi
}
# 同一条命令的**可复制文本**（写进 JSON 与 dry-run 清单，方便人工复现）
adbcmd() {
  if [ -n "$SERIAL" ]; then echo "adb -s $SERIAL shell $1"; else echo "adb shell $1"; fi
}

# stdin → JSON 字符串字面量（不含两侧引号）：多行折成 \n，去掉 adb 的 CRLF
json_str() {
  tr -d '\r' | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e 's/	/\\t/g' \
    | awk 'BEGIN{ORS=""} { if (NR>1) printf "\\n"; print }'
}

TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

# 采集结果（并行数组，最后统一渲染）
ITEM_SEC=(); ITEM_IDS=(); ITEM_STATUS=(); ITEM_CMD=(); ITEM_OUT=(); ITEM_VALUE=(); ITEM_NOTE=()
INCOMPLETE=()

mark_incomplete() {
  local i already=0
  for i in ${INCOMPLETE[@]+"${INCOMPLETE[@]}"}; do [ "$i" = "$1" ] && already=1; done
  [ "$already" = 1 ] || INCOMPLETE+=("$1")
}

# record <sec> <id> <status> <cmd> <out> <value_json> <note>
#   sec:    0 = 设备元信息（不进 sections）、1/2/3 = 文档 §1~§3
#   status: ok | unavailable | skipped | dry-run
#   value:  已是合法 JSON 片段（数字/字符串/对象/null）
record() {
  local out=$5 lines=0
  lines=$(printf '%s\n' "$out" | wc -l | tr -d ' ')
  if [ "$lines" -gt "$MAX_OUT_LINES" ]; then
    out=$(printf '%s\n' "$out" | head -n "$MAX_OUT_LINES")
    out="$out"$'\n'"...(原始输出 $lines 行，已截断到前 $MAX_OUT_LINES 行)"
  fi
  ITEM_SEC+=("$1"); ITEM_IDS+=("$2"); ITEM_STATUS+=("$3")
  ITEM_CMD+=("$4"); ITEM_OUT+=("$out")
  ITEM_VALUE+=("${6:-null}")
  ITEM_NOTE+=("${7:-}")
}

# probe <sec> <id> <shell 命令>  → 输出进 $PROBE_OUT，退出码进 $PROBE_RC
# 有输出且 rc=0 视为采到；否则标 unavailable（不中断脚本）。
PROBE_OUT=""; PROBE_RC=-1
probe() {
  local sec=$1 id=$2 cmd=$3 full
  full=$(adbcmd "$cmd")
  if [ "$DRY_RUN" = 1 ]; then
    printf '  [dry-run] %s\n' "$full"
    PROBE_OUT=""; PROBE_RC=-1
    record "$sec" "$id" dry-run "$full" "" null "--dry-run：只列命令，未执行"
    return 1
  fi
  printf '  $ %s\n' "$full"
  PROBE_OUT=$(adbrun shell "$cmd" 2>&1 | tr -d '\r')
  PROBE_RC=$?
  if [ "$PROBE_RC" -ne 0 ] || [ -z "$PROBE_OUT" ]; then
    record "$sec" "$id" unavailable "$full" "$PROBE_OUT" null \
      "未采到：退出码 $PROBE_RC、输出为空，或该设备无此命令/无权限"
    [ "$sec" = "0" ] || mark_incomplete "$id"
    return 1
  fi
  return 0
}

# act <sec> <id> <shell 命令> [说明]
# 与 probe 的区别：input/am 这类**动作型**命令成功时本来就没输出，只看退出码。
act() {
  local sec=$1 id=$2 cmd=$3 full
  full=$(adbcmd "$cmd")
  if [ "$DRY_RUN" = 1 ]; then
    printf '  [dry-run] %s\n' "$full"
    record "$sec" "$id" dry-run "$full" "" null "${4:---dry-run：只列命令，未执行}"
    return 1
  fi
  printf '  $ %s\n' "$full"
  local out
  out=$(adbrun shell "$cmd" 2>&1 | tr -d '\r')
  local rc=$?
  if [ "$rc" -ne 0 ]; then
    record "$sec" "$id" unavailable "$full" "$out" null "动作未生效：退出码 $rc（设备可能不支持该子命令）"
    mark_incomplete "$id"
    return 1
  fi
  record "$sec" "$id" ok "$full" "$out" "\"exit_code=0\"" "${4:-}"
  return 0
}

# record_ok <sec> <id> <cmd> <out> <value_json> [note]
record_ok() {
  record "$1" "$2" ok "$3" "$4" "$5" "${6:-}"
}

echo "==> Gox 真机验收采集（rpr9zf）"
[ "$DRY_RUN" = 1 ] && echo "==> --dry-run：只列出将要执行的命令，不连设备、不落盘"

# ── 前置自检 ────────────────────────────────────────────────────────────────
# 三条都在动手采数据之前拦住，且每条都给「下一步该干什么」，而不是只报个错。
if [ "$DRY_RUN" = 0 ]; then
  if ! have adb; then
    echo "error: PATH 里找不到 adb —— 真机采集做不了。" >&2
    echo "       请先安装 Android platform-tools：" >&2
    echo "         · Debian/Ubuntu: sudo apt-get install -y android-sdk-platform-tools" >&2
    echo "         · macOS:         brew install android-platform-tools" >&2
    echo "         · Windows:       scoop install adb  （或 Android Studio → SDK Manager → SDK Tools）" >&2
    echo "       装好后 \`adb devices\` 能看到设备再重跑本脚本；只想看命令清单用 --dry-run。" >&2
    exit 2
  fi

  echo "==> 前置自检：adb $(adb --version 2>/dev/null | head -1)"
  DEVICES=$(adb devices 2>&1 | tr -d '\r' | awk 'NR>1 && NF>=2 {print $1"\t"$2}')
  ONLINE=$(printf '%s\n' "$DEVICES" | awk -F'\t' '$2=="device" {print $1}')
  N_ONLINE=$(printf '%s' "$ONLINE" | grep -c . || true)
  N_ALL=$(printf '%s' "$DEVICES" | grep -c . || true)

  if [ "$N_ALL" = 0 ]; then
    echo "error: adb 已就绪，但没有任何设备在线（adb devices 空）。" >&2
    echo "       1) 开发者选项 → USB 调试 打开；2) 换一根能传数据的线（充电线不行）；" >&2
    echo "       3) 桌面跑 \`adb devices\`，出现「<serial>\tdevice」再重跑本脚本。" >&2
    exit 3
  fi
  if [ "$N_ONLINE" = 0 ]; then
    echo "error: 有设备，但没有一台处于 device（可调试）状态：" >&2
    printf '%s\n' "$DEVICES" | awk -F'\t' '{printf "       %s  ← %s\n", $1, $2}' >&2
    echo "       · unauthorized：设备上点「允许 USB 调试」（看清指纹），再 \`adb kill-server && adb devices\`" >&2
    echo "       · offline：拔插一次 / 换口换线 / 重开 USB 调试" >&2
    exit 3
  fi
  if [ "$N_ONLINE" -gt 1 ] && [ -z "$SERIAL" ]; then
    echo "error: 在线设备 $N_ONLINE 台，但没指定用哪台（真机验收一次只验一台）：" >&2
    printf '%s\n' "$ONLINE" | sed 's/^/       /' >&2
    echo "       重跑时加 --device <serial>。" >&2
    exit 3
  fi
  SERIAL=${SERIAL:-$(printf '%s\n' "$ONLINE" | head -1)}
  echo "==> 设备    $SERIAL"
else
  printf '  [dry-run] adb --version\n'
  printf '  [dry-run] adb devices      # 只认状态为 device 的；多台在线需 --device <serial>\n'
fi

# ── 被测包：§3（帧率/内存/冷启动）按包名取数 ──────────────────────────────────
# 没给 --package 时试着从 gox.json 的 appId 兜；再没有就只在 §3 标 skipped，
# §1/§2 照采 —— 一次插设备能拿多少拿多少。
if [ -z "$PKG" ] && [ -f "$ROOT/gox.json" ]; then
  PKG=$(sed -n 's/.*"appId"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$ROOT/gox.json" | head -1)
  [ -n "$PKG" ] && echo "==> 包名    $PKG（来自 gox.json 的 appId）"
fi
if [ -z "$PKG" ]; then
  if [ "$DRY_RUN" = 1 ]; then
    # dry-run 的目的是「把将要执行的命令列全」，所以 §3 也要列出来，用占位符代替包名
    PKG="<pkg>"; ACT="<pkg>/.MainActivity"
    echo "==> 包名    未指定：dry-run 用占位符 <pkg> 列出 §3 命令（真机跑时务必加 --package <appId>）"
  else
    echo "==> 包名    未指定：§3 的帧率/内存/冷启动项将标 skipped（用 --package <appId> 补上）"
  fi
fi

# ── §0 设备元信息 ───────────────────────────────────────────────────────────
echo "==> §0 设备元信息"
M_MODEL=""; M_VENDOR=""; M_ABI=""; M_ABILIST=""; M_REL=""; M_SDK=""; M_BUILD=""
if probe 0 model "getprop ro.product.model";         then M_MODEL=$PROBE_OUT; fi
if probe 0 manufacturer "getprop ro.product.manufacturer"; then M_VENDOR=$PROBE_OUT; fi
if probe 0 abi "getprop ro.product.cpu.abi";         then M_ABI=$PROBE_OUT; fi
if probe 0 abilist "getprop ro.product.cpu.abilist"; then M_ABILIST=$PROBE_OUT; fi
if probe 0 android_version "getprop ro.build.version.release"; then M_REL=$PROBE_OUT; fi
if probe 0 sdk "getprop ro.build.version.sdk";       then M_SDK=$PROBE_OUT; fi
if probe 0 build_id "getprop ro.build.display.id";   then M_BUILD=$PROBE_OUT; fi

# ABI 自检：非 arm64 **不阻断**（x86_64 模拟器也是合法采集目标，见 build-android.sh
# --abi x86_64），但要在摘要里说清楚，免得把模拟器数据当真机基线写进文档。
ABI_WARN=""
if [ "$DRY_RUN" = 0 ] && [ -n "$M_ABI" ]; then
  case "$M_ABI" in
    arm64*) ;;
    *) ABI_WARN="设备 ABI 是 $M_ABI，不是 arm64 —— 数据可用，但**不要**当作真机基线填进 mobile-perf-baseline.md" ;;
  esac
fi

# ── §1 屏幕与密度 ───────────────────────────────────────────────────────────
# 口径：物理 px 与 dpi 都是设备原样；逻辑 dp = px * 160 / dpi（脚本侧换算，
# 与 gfx/mobile 的 Display.Scale 口径一致，见 mobile-perf-baseline.md §1）。
echo "==> §1 屏幕与密度"
DENSITY=""; SIZE=""; PW=""; PH=""; REFR=""; LD_W=""; LD_H=""
if probe 1 wm_density "wm density"; then
  DENSITY=$(printf '%s\n' "$PROBE_OUT" | sed -n 's/.*[Dd]ensity[: ]*\([0-9]*\).*/\1/p' | head -1)
  record 1 wm_density ok "$(adbcmd 'wm density')" "$PROBE_OUT" \
    "$( [ -n "$DENSITY" ] && echo "$DENSITY" || echo null )" "物理/覆盖密度 dpi"
fi
if probe 1 wm_size "wm size"; then
  SIZE=$(printf '%s\n' "$PROBE_OUT" | sed -n 's/.*[Ss]ize[: ]*\([0-9]*x[0-9]*\).*/\1/p' | head -1)
  PW=${SIZE%x*}; PH=${SIZE#*x}
  record 1 wm_size ok "$(adbcmd 'wm size')" "$PROBE_OUT" \
    "$( [ -n "$SIZE" ] && echo "\"$SIZE\"" || echo null )" "物理分辨率 WxH（px）"
fi
if [ -n "$PW" ] && [ -n "$PH" ] && [ -n "$DENSITY" ] && [ "$DENSITY" -gt 0 ] 2>/dev/null; then
  LD_W=$(awk -v w="$PW" -v d="$DENSITY" 'BEGIN{printf "%d", w*160/d}')
  LD_H=$(awk -v h="$PH" -v d="$DENSITY" 'BEGIN{printf "%d", h*160/d}')
  record 1 logical_dp ok "(由 wm size 与 wm density 换算)" "logical = px * 160 / dpi = ${PW}x${PH} * 160 / ${DENSITY}" \
    "{\"width_dp\": $LD_W, \"height_dp\": $LD_H, \"formula\": \"px * 160 / dpi\"}" \
    "逻辑尺寸（dp）：断点判定用的就是这个，不是物理 px"
else
  record 1 logical_dp skipped "(由 wm size 与 wm density 换算)" "" null \
    "缺 wm size 或 wm density，没法换算逻辑 dp"
  mark_incomplete logical_dp
fi
if probe 1 refresh_rate "dumpsys display | grep -m1 -i refreshrate"; then
  REFR=$(printf '%s\n' "$PROBE_OUT" | grep -oE '[0-9]+\.[0-9]+|[0-9]+' | head -1)
  record 1 refresh_rate ok "$(adbcmd 'dumpsys display | grep -m1 -i refreshrate')" "$PROBE_OUT" \
    "$( [ -n "$REFR" ] && echo "$REFR" || echo null )" "刷新率 Hz（帧率上限，§3 的 fps 要对着它看）"
fi

# ── §2 触摸与多指手势 ───────────────────────────────────────────────────────
# 注入坐标用物理 px 的中点；采不到尺寸就退回一组保守坐标并在 note 里说明。
CX=${PW:-540}; CY=${PH:-1200}
CX=$(( CX / 2 )); CY=$(( CY / 2 ))
if [ -n "$PW" ]; then CX=$(( PW / 2 )); CY=$(( PH / 2 )); fi
GE_LOG="$TMP_DIR/getevent.log"; : > "$GE_LOG"

echo "==> §2 触摸与多指手势"
# getevent 采样在**宿主后台**跑，注入动作期间抓内核事件做证据（能采多少采多少：
# 无 timeout / 无权限 / 无事件时如实标 unavailable，不伪造「已验证多指」）。
if [ "$DRY_RUN" = 0 ]; then
  printf '  $ %s   （后台采样 %ss，注入期间抓内核输入事件）\n' "$(adbcmd "timeout $SAMPLE_SEC getevent -l -c 200")" "$SAMPLE_SEC"
  ( adbrun shell "timeout $SAMPLE_SEC getevent -l -c 200" > "$GE_LOG" 2>&1 ) &
  GE_PID=$!
else
  printf '  [dry-run] %s\n' "$(adbcmd "timeout $SAMPLE_SEC getevent -l -c 200")"
fi

act 2 tap "input tap $CX $CY" "单点：屏幕中心 tap（$CX,$CY px）"
act 2 swipe "input swipe $CX $(( CY + 300 )) $CX $(( CY - 300 )) 400" "单指滑动：向上滚一屏（400ms）"
# 双指张开/捏合：Android 12+ 的 input motionevent 支持多指；老版本会报
# 未知子命令 —— 那时只能人工验，脚本如实标 unavailable，绝不"当作通过"。
act 2 pinch "input motionevent DOWN $(( CX - 120 )) $CY; input motionevent POINTER_DOWN $(( CX + 120 )) $CY; input motionevent MOVE $(( CX - 240 )) $CY; input motionevent MOVE $(( CX + 240 )) $CY; input motionevent POINTER_UP $(( CX + 240 )) $CY; input motionevent UP $(( CX - 240 )) $CY" \
  "双指张开（两指从 ±120px 移到 ±240px）；设备 Android < 12 时 input motionevent 不可用"

if [ "$DRY_RUN" = 0 ]; then
  # 等采样结束，但最多多给 8 秒兜底 —— 真机上 timeout 缺失时 getevent 会一直阻塞，
  # 直接 wait 会把脚本挂死（挂死比缺一项数据更难救）。
  i=0
  while [ "$i" -lt "$(( SAMPLE_SEC + 8 ))" ]; do
    kill -0 "$GE_PID" 2>/dev/null || break
    sleep 1; i=$(( i + 1 ))
  done
  kill "$GE_PID" 2>/dev/null
  wait "$GE_PID" 2>/dev/null

  GE_TXT=$(cat "$GE_LOG" 2>/dev/null | tr -d '\r')
  if [ -z "$GE_TXT" ]; then
    record 2 getevent unavailable "$(adbcmd "timeout $SAMPLE_SEC getevent -l -c 200")" "$GE_TXT" null \
      "内核事件没抓到：设备可能没有 timeout、/dev/input 无权限，或采样窗口内没有输入事件"
    mark_incomplete getevent
  else
    SLOTS=$(printf '%s\n' "$GE_TXT" | grep -c 'ABS_MT_SLOT' || true)
    TRACK=$(printf '%s\n' "$GE_TXT" | grep -c 'ABS_MT_TRACKING_ID' || true)
    TAPS=$(printf '%s\n' "$GE_TXT" | grep -ci 'BTN_TOUCH' || true)
    record 2 getevent ok "$(adbcmd "timeout $SAMPLE_SEC getevent -l -c 200")" "$GE_TXT" \
      "{\"lines\": $(printf '%s\n' "$GE_TXT" | wc -l | tr -d ' '), \"abs_mt_slot_hits\": $SLOTS, \"abs_mt_tracking_id_hits\": $TRACK, \"btn_touch_hits\": $TAPS}" \
      "多指证据看 ABS_MT_SLOT / ABS_MT_TRACKING_ID；为 0 说明只有单指事件被上报"
  fi
else
  record 2 getevent dry-run "$(adbcmd "timeout $SAMPLE_SEC getevent -l -c 200")" "" null "--dry-run：未执行"
fi

if probe 2 input_dispatcher "dumpsys input | grep -iE 'MotionEvent|InputDispatcher|touch' | head -20"; then
  record_ok 2 input_dispatcher "$(adbcmd "dumpsys input | grep -iE 'MotionEvent|InputDispatcher|touch' | head -20")" \
    "$PROBE_OUT" "\"见原始输出\"" "派发侧证据（不同厂商字段差异大，仅存档）"
fi

# ── §3 帧率 / 渲染耗时 / 内存 / 冷启动 ───────────────────────────────────────
echo "==> §3 帧率、内存与冷启动"
skip3() { # skip3 <id> <命令> <为什么>
  record 3 "$1" skipped "$(adbcmd "$2")" "" null "$3"
  mark_incomplete "$1"
  printf '  [skip]   %s（%s）\n' "$1" "$3"
}

if [ -z "$PKG" ]; then
  skip3 gfxinfo_reset "dumpsys gfxinfo <pkg> reset" "未提供被测包（--package 或 gox.json 的 appId）"
  skip3 gfxinfo_stats "dumpsys gfxinfo <pkg>" "同上"
  skip3 meminfo "dumpsys meminfo <pkg>" "同上"
  skip3 cold_start "am start -W -n <pkg>/<activity>" "同上"
else
  # 冷启动：先 force-stop 保证是冷启动，再用 am start -W 读 TotalTime（ms）。
  if [ -z "$ACT" ]; then
    probe 3 resolve_activity "cmd package resolve-activity --brief -c android.intent.category.LAUNCHER $PKG" \
      && record_ok 3 resolve_activity "$(adbcmd "cmd package resolve-activity --brief -c android.intent.category.LAUNCHER $PKG")" "$PROBE_OUT" "\"见原始输出\"" "解析启动 Activity"
    CAND=$(printf '%s\n' "${PROBE_OUT:-}" | tail -1 | tr -d ' ')
    case "$CAND" in
      */*) ACT=$CAND ;;
      *)   ACT="$PKG/.MainActivity"; echo "==> 启动 Activity 用兜底值 $ACT（可用 --activity 覆盖）" ;;
    esac
  fi
  case "$ACT" in
    */*) ;;                  # 已自带包名：com.x/.Main
    .*)  ACT="$PKG$ACT" ;;   # .MainActivity → com.x.MainActivity
    *)   ACT="$PKG/$ACT" ;;  # MainActivity  → com.x/MainActivity
  esac
  echo "==> 启动组件 $ACT"

  act 3 force_stop "am force-stop $PKG" "确保下一次启动是冷启动"
  if probe 3 cold_start "am start -W -n $ACT"; then
    TOTAL=$(printf '%s\n' "$PROBE_OUT" | sed -n 's/.*TotalTime: *\([0-9]*\).*/\1/p' | head -1)
    WAIT=$(printf '%s\n' "$PROBE_OUT" | sed -n 's/.*WaitTime: *\([0-9]*\).*/\1/p' | head -1)
    if [ -n "$TOTAL" ]; then
      record 3 cold_start ok "$(adbcmd "am start -W -n $ACT")" "$PROBE_OUT" \
        "{\"total_time_ms\": $TOTAL, \"wait_time_ms\": $( [ -n "$WAIT" ] && echo "$WAIT" || echo null ), \"component\": \"$ACT\"}" \
        "冷启动 TotalTime（ms，含进程启动+首帧），口径见 mobile-perf-baseline.md §3"
    else
      record 3 cold_start unavailable "$(adbcmd "am start -W -n $ACT")" "$PROBE_OUT" null \
        "am start -W 没回 TotalTime（组件可能不存在/未安装：$ACT）"
      mark_incomplete cold_start
    fi
  fi

  # 帧率：reset 计数器 → 窗口内持续注入滑动制造负载 → 读聚合值。
  # 注意：这是**粗估** fps（渲染帧数 / 墙钟窗口），不等于 gfxinfo 的逐帧直方图；
  # 百分比分位数随设备/系统版本有无而有无，读不到就写 null。
  act 3 gfxinfo_reset "dumpsys gfxinfo $PKG reset" "清零帧统计，开始 ${SAMPLE_SEC}s 采样窗口"
  if [ "$DRY_RUN" = 0 ]; then
    n=0
    while [ "$n" -lt "$(( SAMPLE_SEC * 2 ))" ]; do
      adbrun shell "input swipe $CX $(( CY + 300 )) $CX $(( CY - 300 )) 300" >/dev/null 2>&1
      n=$(( n + 1 ))
    done
  else
    printf '  [dry-run] adb shell input swipe ...   （采样窗口内每 0.5s 注入一次滑动，共约 %s 次）\n' "$(( SAMPLE_SEC * 2 ))"
  fi
  if probe 3 gfxinfo_stats "dumpsys gfxinfo $PKG"; then
    FRAMES=$(printf '%s\n' "$PROBE_OUT" | sed -n 's/.*Total frames rendered: *\([0-9]*\).*/\1/p' | head -1)
    JANKY=$(printf '%s\n' "$PROBE_OUT" | sed -n 's/.*Janky frames: *\([0-9]*\).*/\1/p' | head -1)
    P50=$(printf '%s\n' "$PROBE_OUT" | sed -n 's/.*50th percentile: *\([0-9.]*\).*/\1/p' | head -1)
    P95=$(printf '%s\n' "$PROBE_OUT" | sed -n 's/.*95th percentile: *\([0-9.]*\).*/\1/p' | head -1)
    P99=$(printf '%s\n' "$PROBE_OUT" | sed -n 's/.*99th percentile: *\([0-9.]*\).*/\1/p' | head -1)
    FPS="null"
    if [ -n "$FRAMES" ] && [ "$SAMPLE_SEC" -gt 0 ]; then
      FPS=$(awk -v f="$FRAMES" -v s="$SAMPLE_SEC" 'BEGIN{printf "%.1f", f/s}')
    fi
    if [ -n "$FRAMES" ] || [ -n "$P50" ]; then
      record 3 gfxinfo_stats ok "$(adbcmd "dumpsys gfxinfo $PKG")" "$PROBE_OUT" \
        "{\"sample_sec\": $SAMPLE_SEC, \"total_frames\": $( [ -n "$FRAMES" ] && echo "$FRAMES" || echo null ), \"janky_frames\": $( [ -n "$JANKY" ] && echo "$JANKY" || echo null ), \"fps_estimate\": $FPS, \"p50_ms\": $( [ -n "$P50" ] && echo "$P50" || echo null ), \"p95_ms\": $( [ -n "$P95" ] && echo "$P95" || echo null ), \"p99_ms\": $( [ -n "$P99" ] && echo "$P99" || echo null )}" \
        "fps_estimate 是渲染帧数/采样窗口的粗估，不是逐帧直方图；分位数缺失是设备没输出，未填充"
    else
      record 3 gfxinfo_stats unavailable "$(adbcmd "dumpsys gfxinfo $PKG")" "$PROBE_OUT" null \
        "gfxinfo 没给出帧统计（包名没在跑？或该 Android 版本裁剪了输出）"
      mark_incomplete gfxinfo_stats
    fi
  fi

  if probe 3 meminfo "dumpsys meminfo $PKG"; then
    PSS=$(printf '%s\n' "$PROBE_OUT" | grep -E '^[[:space:]]*TOTAL' | head -1 | grep -oE '[0-9]+' | head -1)
    if [ -n "$PSS" ]; then
      record 3 meminfo ok "$(adbcmd "dumpsys meminfo $PKG")" "$PROBE_OUT" \
        "{\"pss_kb\": $PSS, \"unit\": \"kB\"}" \
        "PSS 取 TOTAL 行首个数字（kB）；对比开发机 bench 的 rss_kb 时口径不同，见 baseline 文档 §4"
    else
      record 3 meminfo unavailable "$(adbcmd "dumpsys meminfo $PKG")" "$PROBE_OUT" null \
        "meminfo 里没有 TOTAL 行（进程没在跑，或包名不对）"
      mark_incomplete meminfo
    fi
  fi
fi

# ── 落盘 JSON ───────────────────────────────────────────────────────────────
# render_section <sec> <json key> <是否补逗号>
render_section() {
  local sec=$1 key=$2 comma=$3 i first=1 cmd out note
  printf '    %s: {\n      "items": [\n' "$key"
  if [ ${#ITEM_IDS[@]} -gt 0 ]; then
    for i in "${!ITEM_IDS[@]}"; do
      [ "${ITEM_SEC[$i]}" = "$sec" ] || continue
      if [ "$first" = 0 ]; then printf ',\n'; fi
      first=0
      cmd=$(printf '%s' "${ITEM_CMD[$i]}" | json_str)
      out=$(printf '%s' "${ITEM_OUT[$i]}" | json_str)
      note=$(printf '%s' "${ITEM_NOTE[$i]}" | json_str)
      printf '        {"id": "%s", "status": "%s", "command": "%s", "output": "%s", "value": %s, "note": "%s"}' \
        "${ITEM_IDS[$i]}" "${ITEM_STATUS[$i]}" "$cmd" "$out" "${ITEM_VALUE[$i]}" "$note"
    done
  fi
  if [ "$first" = 0 ]; then printf '\n'; fi
  printf '      ]\n    }'
  [ "$comma" = 1 ] && printf ','
  printf '\n'
}

DATE_STAMP=$(date -u +%Y-%m-%dT%H:%M:%SZ)
DAY=$(date +%F)
SLUG=$(printf '%s' "${M_MODEL:-${SERIAL:-dry-run}}" | tr -c 'A-Za-z0-9._-' '_' | sed 's/_*$//')
[ -n "$SLUG" ] || SLUG="unknown"
OUT_JSON="$OUT_DIR/device-accept-$DAY-$SLUG.json"

if [ "$DRY_RUN" = 1 ]; then
  echo
  echo "==> --dry-run 结束：以上是接上设备后会执行的全部命令（未连设备、未落盘）。"
  echo "    真机到位后跑 \`bash scripts/mobile-device-accept.sh\` 会把结果写到："
  echo "      ${OUT_DIR#"$ROOT"/}/device-accept-<yyyy-mm-dd>-<device>.json"
  exit 0
fi

mkdir -p "$OUT_DIR"
{
  printf '{\n'
  printf '  "schema": "gox.device-accept/v1",\n'
  printf '  "generated_at": "%s",\n' "$DATE_STAMP"
  printf '  "mode": "adb",\n'
  printf '  "doc": "docs/mobile-perf-baseline.md",\n'
  printf '  "device": {\n'
  printf '    "serial": "%s",\n' "$(printf '%s' "$SERIAL" | json_str)"
  printf '    "manufacturer": "%s",\n' "$(printf '%s' "$M_VENDOR" | json_str)"
  printf '    "model": "%s",\n' "$(printf '%s' "$M_MODEL" | json_str)"
  printf '    "abi": "%s",\n' "$(printf '%s' "$M_ABI" | json_str)"
  printf '    "abi_list": "%s",\n' "$(printf '%s' "$M_ABILIST" | json_str)"
  printf '    "android_version": "%s",\n' "$(printf '%s' "$M_REL" | json_str)"
  printf '    "sdk": "%s",\n' "$(printf '%s' "$M_SDK" | json_str)"
  printf '    "build_id": "%s",\n' "$(printf '%s' "$M_BUILD" | json_str)"
  printf '    "abi_is_arm64": %s\n' "$(case "$M_ABI" in arm64*) echo true;; *) echo false;; esac)"
  printf '  },\n'
  printf '  "target": {\n'
  printf '    "package": "%s",\n' "$(printf '%s' "$PKG" | json_str)"
  printf '    "activity": "%s",\n' "$(printf '%s' "$ACT" | json_str)"
  printf '    "sample_sec": %s\n' "$SAMPLE_SEC"
  printf '  },\n'
  printf '  "sections": {\n'
  render_section 1 '"1_display_density"' 1
  render_section 2 '"2_touch_gesture"' 1
  render_section 3 '"3_perf"' 0
  printf '  },\n'
  printf '  "incomplete": ['
  if [ ${#INCOMPLETE[@]} -gt 0 ]; then
    printf '\n'
    i=0
    for i in "${!INCOMPLETE[@]}"; do
      [ "$i" = 0 ] || printf ',\n'
      printf '    "%s"' "${INCOMPLETE[$i]}"
    done
    printf '\n  '
  fi
  printf ']\n}\n'
} > "$OUT_JSON"

# ── 人读摘要 ────────────────────────────────────────────────────────────────
val_of() { # val_of <id>：把某项采集值从 JSON 里捞出来（只为打印，失败不影响退出码）
  grep -o "\"id\": \"$1\".*" "$OUT_JSON" 2>/dev/null | head -1 | sed -n 's/.*"value": \(.*\), "note".*/\1/p'
}
num_of() { # num_of <id> <字段名>：从 value 对象里取一个数字（字段名本身含数字，不能用 grep -o '[0-9]*'）
  val_of "$1" | tr ',' '\n' | sed -n "s/.*\"$2\": *\([0-9.]*\).*/\1/p" | head -1
}
show() { # show <取到的值> <缺省文案>：null / 空一律按「没采到」处理，不把 null 当数字印出来
  case "$1" in ""|null) printf '%s' "$2" ;; *) printf '%s' "$1" ;; esac
}
rc_show() { # rc_show <id>：动作型项只看退出码，0 = 注入成功
  case "$(val_of "$1")" in *exit_code=0*) printf '已注入' ;; *) printf '未生效' ;; esac
}
echo
echo "==> 结果   ${OUT_JSON#"$ROOT"/}"
echo "─────────────────────────────────────────────────────────────"
printf '  设备      %s %s（%s）\n' "${M_VENDOR:-?}" "${M_MODEL:-?}" "${SERIAL:-?}"
printf '  ABI       %s%s\n' "${M_ABI:-?}" "$( [ -n "$ABI_WARN" ] && echo "  ← 非 arm64" || echo )"
printf '  Android   %s（SDK %s）\n' "${M_REL:-?}" "${M_SDK:-?}"
printf '  §1 密度   %s dpi   物理 %s   逻辑 %s dp   刷新 %s Hz\n' \
  "${DENSITY:-待采集}" "${SIZE:-待采集}" "$( [ -n "$LD_W" ] && echo "${LD_W}x${LD_H}" || echo "待采集" )" "${REFR:-待采集}"
printf '  §2 触控   tap=%s swipe=%s pinch=%s   多指证据: %s\n' \
  "$(rc_show tap)" "$(rc_show swipe)" "$(rc_show pinch)" \
  "$(show "$(val_of getevent | tr -d '\n' | cut -c1-80)" '未采到（getevent 无输出/无权限）')"
printf '  §3 冷启动 %s ms\n' "$(show "$(num_of cold_start total_time_ms)" 待采集)"
printf '  §3 帧率   %s fps（帧 %s / 掉帧 %s / p95 %s ms）\n' \
  "$(show "$(num_of gfxinfo_stats fps_estimate)" 待采集)" \
  "$(show "$(num_of gfxinfo_stats total_frames)" '?')" \
  "$(show "$(num_of gfxinfo_stats janky_frames)" '?')" \
  "$(show "$(num_of gfxinfo_stats p95_ms)" '?')"
printf '  §3 内存   %s kB（PSS）\n' "$(show "$(num_of meminfo pss_kb)" 待采集)"
echo "─────────────────────────────────────────────────────────────"

if [ -n "$ABI_WARN" ]; then
  echo "⚠️  $ABI_WARN"
fi

if [ ${#INCOMPLETE[@]} -gt 0 ]; then
  echo
  echo "本次未完成项（${#INCOMPLETE[@]}）："
  for i in "${INCOMPLETE[@]}"; do
    why=$(grep -o "\"id\": \"$i\".*" "$OUT_JSON" 2>/dev/null | head -1 | sed -n 's/.*"note": "\(.*\)"}[,]*$/\1/p')
    echo "  · $i —— ${why:-见 JSON 里同 id 项的 note 字段}"
  done
  echo "补采做法：把对应命令在设备上手跑一遍，手工把数字填进 docs/mobile-perf-baseline.md 的表里并注明「手工补采」。"
  exit 1
fi

echo "采集完成：§1~§3 全部取到。把数字回填 docs/mobile-perf-baseline.md 的空表（写清机型/系统/日期）。"
exit 0
