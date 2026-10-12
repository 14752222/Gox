#!/usr/bin/env bash
# RSS 曲线采集（r95WjJ）：xvfb 下起一个 GUI 常驻进程，用 tools/rssmon 按间隔
# 采样其进程树 RSS，产物落 bench-results/rss-<目标>-<UTC>.csv。
#
#   bash scripts/rss-curve.sh                 # 默认 60s / 1s 间隔 / notes dev
#   DURATION=120 INTERVAL=2 bash scripts/rss-curve.sh
#   RSS_TARGET_CMD="./gox apps/notes/src/main.js" bash scripts/rss-curve.sh
#
# 为什么默认目标是 `gox dev` 而不是直接 `gox <entry>`：
#   2026-10-11 实测（本仓库 main + Xvfb）：JS 侧 render() 建出的 x11 窗口在
#   首个 Pump 节拍就被判定连接关闭（gfx/x11 errch 立即有值），进程随即退出
#   —— 纯 Go 路径 gfx.Mount+Pump 同样环境下存活正常，是 JS→gfx 链路的既有
#   缺陷，与本工具无关。`gox dev` 的 watcher 父进程是真常驻的，RSS 曲线先
#   覆盖「常驻宿主 + 渲染链路启动足迹」；窗口泵缺陷修复后把 RSS_TARGET_CMD
#   换成直跑入口即可得到纯 GUI 曲线。
set -euo pipefail
cd "$(dirname "$0")/.."

DURATION=${DURATION:-60}
INTERVAL=${INTERVAL:-1}
APP=${APP:-apps/notes/src/main.js}
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
$RUNNER "$BIN/rssmon" -interval "${INTERVAL}s" -duration "${DURATION}s" \
    -out "$CSV" -- $CMD

echo ">>> 产物: $CSV"
head -5 "$CSV"
