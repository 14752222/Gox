#!/usr/bin/env bash
# RSS 曲线采集（r95WjJ）+ 常驻内存闸门（T3-3）：xvfb 下起一个 GUI 常驻进程，
# 用 tools/rssmon 按间隔采样其进程树 RSS，产物落 bench-results/rss-<目标>-<UTC>.csv。
#
#   bash scripts/rss-curve.sh                 # 默认 60s / 1s 间隔 / notes dev
#   DURATION=120 INTERVAL=2 bash scripts/rss-curve.sh
#   RSS_TARGET_CMD="./gox apps/notes/src/main.js" bash scripts/rss-curve.sh
#   bash scripts/rss-curve.sh --max-rss 196608 --max-slope 512
#
# 为什么默认目标是 `gox dev` 而不是直接 `gox <entry>`：
#   2026-10-11 实测（本仓库 main + Xvfb）：JS 侧 render() 建出的 x11 窗口在
#   首个 Pump 节拍就被判定连接关闭（gfx/x11 errch 立即有值），进程随即退出
#   —— 纯 Go 路径 gfx.Mount+Pump 同样环境下存活正常，是 JS→gfx 链路的既有
#   缺陷，与本工具无关。`gox dev` 的 watcher 父进程是真常驻的，RSS 曲线先
#   覆盖「常驻宿主 + 渲染链路启动足迹」；窗口泵缺陷修复后把 RSS_TARGET_CMD
#   换成直跑入口即可得到纯 GUI 曲线。
#
# ── 闸门（T3-3）─────────────────────────────────────────────────────────────
# 只采集曲线回答不了「能不能下班」：没人看 artifact 就等于没有回归判据。
# 两个上限，任一越界退出码 1（参数/环境错误退出码 2，与"超限"区分开）：
#
#   --max-rss   <KB>      峰值 RSS 上限（进程树合计，含 gox dev 的父子进程）
#   --max-slope <KB/min>  稳态段 RSS 斜率上限（最小二乘，见下方"稳态段"）
#
# 默认值：两个都不设 = 只采曲线不判（nightly 曲线用这条，保持绿；绝对 RSS
# 随机器/镜像/字体栈漂移，硬判会制造噪声性红，见 rss-curve.yml 头部）。
#
# 阈值怎么来的（2026-10-28 本仓库 agent/a6 实测，Xvfb + 软件渲染，
# `gox dev apps/notes/src/main.js`，DURATION=40 INTERVAL=1）：
#   峰值 130664 kB ≈ 127.6 MB，稳态斜率 +0.0 kB/min（1s 后即平台期）。
#   推荐值取「实测基线 + 余量」：
#     --max-rss   196608 kB（192 MB）≈ 基线 ×1.5，容纳不同字体栈/驱动差异
#     --max-slope 512 kB/min ≈ 0.5 MB/min；8 小时挂机累计 ≤ 240 MB，
#                 落在基线之上但仍能拦住"每帧漏一点"的量级
#   斜率比峰值更能抓泄漏：无 GC 设计的泄漏不表现为崩溃，只表现为 RSS 单调
#   上行；而启动足迹是一次性的，会在前若干秒内冲高后转平。
#
# 「稳态段」为什么砍掉前 30%：启动期是"一次性的线性爬升"（加载 JS、建窗口、
#   解码图标/字体），拿全段做回归会被这段拖成一条假斜率。默认跳过前 30% 的
#   样本再回归；样本数 < 10 时不做跳过（数据点太少，跳过反而更不稳）。
#
# 机器可读摘要：退出前在 stderr 打一组 KEY=VALUE，并打一行单行 JSON
#   （前缀 RSS_SUMMARY），CI 可以直接 grep / 解析。
set -euo pipefail
cd "$(dirname "$0")/.."

DURATION=${DURATION:-60}
INTERVAL=${INTERVAL:-1}
APP=${APP:-apps/notes/src/main.js}

# 阈值也能用环境变量给（CI 里比拼命令行方便），命令行同名参数优先。
MAX_RSS_KB=${RSS_MAX_KB:-}
MAX_SLOPE_KB_PER_MIN=${RSS_MAX_SLOPE_KB_PER_MIN:-}

usage() {
    cat <<'EOF'
用法: bash scripts/rss-curve.sh [--max-rss <KB>] [--max-slope <KB/min>]

  --max-rss <KB>        峰值 RSS 上限，越界退出码 1
  --max-slope <KB/min>  稳态段 RSS 斜率上限，越界退出码 1
  -h, --help            显示本说明

环境变量: DURATION / INTERVAL / APP / RSS_TARGET_CMD / RUNNER /
          RSS_MAX_KB / RSS_MAX_SLOPE_KB_PER_MIN
EOF
}

is_number() {
    # 只接受非负整数或小数；阈值写错应当立刻报错，而不是被 awk 静默当 0。
    [[ $1 =~ ^[0-9]+([.][0-9]+)?$ ]]
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --max-rss)
            [[ $# -ge 2 ]] || { echo "--max-rss 需要一个值" >&2; exit 2; }
            MAX_RSS_KB=$2; shift 2 ;;
        --max-rss=*) MAX_RSS_KB=${1#*=}; shift ;;
        --max-slope)
            [[ $# -ge 2 ]] || { echo "--max-slope 需要一个值" >&2; exit 2; }
            MAX_SLOPE_KB_PER_MIN=$2; shift 2 ;;
        --max-slope=*) MAX_SLOPE_KB_PER_MIN=${1#*=}; shift ;;
        -h|--help) usage; exit 0 ;;
        *) echo "未知参数: $1" >&2; usage >&2; exit 2 ;;
    esac
done

[[ -z "$MAX_RSS_KB" ]] || is_number "$MAX_RSS_KB" || { echo "--max-rss 不是数字: $MAX_RSS_KB" >&2; exit 2; }
[[ -z "$MAX_SLOPE_KB_PER_MIN" ]] || is_number "$MAX_SLOPE_KB_PER_MIN" || { echo "--max-slope 不是数字: $MAX_SLOPE_KB_PER_MIN" >&2; exit 2; }

TS=$(date -u +%Y%m%dT%H%M%SZ)
mkdir -p bench-results
CSV="bench-results/rss-notes-${TS}.csv"

BIN=$(mktemp -d)
trap 'rm -rf "$BIN"' EXIT
echo ">>> 构建 gox 与 rssmon（$BIN，退出即清理，不进 bench-results）"
go build -o "$BIN/gox" ./cmd/gox
go build -o "$BIN/rssmon" ./tools/rssmon

RUNNER=${RUNNER:-xvfb-run -a}
CMD=${RSS_TARGET_CMD:-"$BIN/gox dev $APP"}

echo ">>> 采集 ${DURATION}s（间隔 ${INTERVAL}s）：$CMD"
# shellcheck disable=SC2086
set +e
$RUNNER "$BIN/rssmon" -interval "${INTERVAL}s" -duration "${DURATION}s" \
    -out "$CSV" -- $CMD
RSSMON_RC=$?
set -e

if [[ ! -s "$CSV" ]]; then
    echo ">>> rssmon 没有产出 CSV（退出码 $RSSMON_RC），无法判定" >&2
    exit 2
fi
# 只有表头 = 一个样本都没采到：判定无意义，按环境错误处理。
if [[ $(wc -l < "$CSV") -le 1 ]]; then
    echo ">>> CSV 只有表头，没有样本（rssmon 退出码 $RSSMON_RC）" >&2
    exit 2
fi

echo ">>> 产物: $CSV"
head -5 "$CSV"

# 统计：首/末/峰值 + 稳态段最小二乘斜率（kB/min）。
# awk 只用 POSIX 语法（ubuntu 上是 mawk，不支持 gawk 扩展）。
read -r N FIRST LAST PEAK SLOPE WARMUP < <(awk -F, '
    NR == 1 { next }
    {
        n++; t[n] = $1 + 0; r[n] = $2 + 0
        if (n == 1) first = r[n]
        if (r[n] > peak) peak = r[n]
        last = r[n]
    }
    END {
        if (n == 0) { print "0 0 0 0 0 0"; exit }
        w = int(n * 0.3)
        if (n - w < 4) w = 0        # 样本太少时不做"去启动段"，否则回归只剩两三个点
        m = 0
        for (i = w + 1; i <= n; i++) { m++; st += t[i]; sr += r[i] }
        mt = st / m; mr = sr / m
        num = 0; den = 0
        for (i = w + 1; i <= n; i++) { dt = t[i] - mt; num += dt * (r[i] - mr); den += dt * dt }
        slope = (den > 0) ? num / den * 60 : 0
        printf "%d %.0f %.0f %.0f %.1f %d\n", n, first, last, peak, slope, w
    }' "$CSV")

# 判定：未设阈值的一侧恒为通过（null 写进 JSON，别用 0 冒充"设过 0"）。
RSS_OK=true
SLOPE_OK=true
if [[ -n "$MAX_RSS_KB" ]]; then
    RSS_OK=$(awk -v p="$PEAK" -v m="$MAX_RSS_KB" 'BEGIN { print (p <= m) ? "true" : "false" }')
fi
if [[ -n "$MAX_SLOPE_KB_PER_MIN" ]]; then
    SLOPE_OK=$(awk -v s="$SLOPE" -v m="$MAX_SLOPE_KB_PER_MIN" 'BEGIN { print (s <= m) ? "true" : "false" }')
fi

VERDICT=pass
[[ "$RSS_OK" == true && "$SLOPE_OK" == true ]] || VERDICT=fail

echo ">>> RSS 摘要: 样本 ${N}，首 ${FIRST} kB，末 ${LAST} kB，峰值 ${PEAK} kB，稳态斜率 ${SLOPE} kB/min（跳过前 ${WARMUP} 个样本）"
echo "RSS_CSV=$CSV"
echo "RSS_SAMPLES=$N"
echo "RSS_FIRST_KB=$FIRST"
echo "RSS_LAST_KB=$LAST"
echo "RSS_PEAK_KB=$PEAK"
echo "RSS_SLOPE_KB_PER_MIN=$SLOPE"
echo "RSS_MAX_KB=${MAX_RSS_KB:--}"
echo "RSS_MAX_SLOPE_KB_PER_MIN=${MAX_SLOPE_KB_PER_MIN:--}"
echo "RSS_RSS_OK=$RSS_OK"
echo "RSS_SLOPE_OK=$SLOPE_OK"
echo "RSS_VERDICT=$VERDICT"
# 单行 JSON 供 CI 解析（路径里不会有引号：bench-results/rss-notes-<UTC>.csv）。
printf 'RSS_SUMMARY {"csv":"%s","samples":%s,"first_kb":%s,"last_kb":%s,"peak_kb":%s,"slope_kb_per_min":%s,"max_rss_kb":%s,"max_slope_kb_per_min":%s,"rss_ok":%s,"slope_ok":%s,"pass":%s}\n' \
    "$CSV" "$N" "$FIRST" "$LAST" "$PEAK" "$SLOPE" \
    "${MAX_RSS_KB:-null}" "${MAX_SLOPE_KB_PER_MIN:-null}" "$RSS_OK" "$SLOPE_OK" "$([[ $VERDICT == pass ]] && echo true || echo false)"

if [[ $VERDICT != pass ]]; then
    echo ">>> RSS 闸门未通过：peak ${PEAK} kB（上限 ${MAX_RSS_KB:--}），slope ${SLOPE} kB/min（上限 ${MAX_SLOPE_KB_PER_MIN:--}）" >&2
    exit 1
fi
echo ">>> RSS 闸门通过"
