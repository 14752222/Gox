#!/usr/bin/env bash
# ============================================================================
# bench-all.sh — 一条命令跑完 M9 的全部性能基准
#
#   ./scripts/bench-all.sh                 # 全部 (runs=3)
#   ./scripts/bench-all.sh --runs 5        # 对比表每项跑 5 次取中位
#   ./scripts/bench-all.sh --no-perf       # 只跑 gox/node/goja 对比表
#   ./scripts/bench-all.sh --perf-only     # 只跑帧率/GC 毛刺基准
#
# 两件事:
#   1. scripts/bench-compare.sh  —— 解释器算力对比 (gox / node / goja / ...)
#   2. go test ./perf/           —— UI 帧率 + GC 毛刺基准
#
# 联网要求:
#   - gox / node 部分**完全离线可跑**;
#   - goja 部分需要 bench/goja 依赖已下载 (一次: cd bench/goja && go mod download);
#     未下载时 bench-compare.sh 会**自动跳过 goja 栈**, 不影响其余部分。
#
# 产物全部落在 bench-results/ (bench-*.json 与 perf-*.json)。
# ============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RUNS=3
DO_COMPARE=1
DO_PERF=1
while [[ $# -gt 0 ]]; do
  case "$1" in
    --runs) RUNS="$2"; shift 2 ;;
    --no-perf) DO_PERF=0; shift ;;
    --perf-only) DO_COMPARE=0; shift ;;
    -h|--help) sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "未知参数 $1 (见 --help)"; exit 1 ;;
  esac
done

echo "==================================================================="
echo "[bench-all] 机器信息 (写进结果的复现依据)"
echo "  时间(UTC):   $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "  OS:          $(uname -srm)"
if [[ "$(uname -s)" == "Darwin" ]]; then
  echo "  CPU:         $(sysctl -n machdep.cpu.brand_string 2>/dev/null) / $(sysctl -n hw.ncpu 2>/dev/null) 核"
  echo "  内存:        $(( $(sysctl -n hw.memsize 2>/dev/null) / 1024 / 1024 )) MB"
fi
if [[ "$(uname -s)" == "Linux" ]]; then
  echo "  CPU:         $(/usr/bin/grep -m1 'model name' /proc/cpuinfo 2>/dev/null | cut -d: -f2 | xargs) / $(nproc 2>/dev/null) 核"
fi
echo "  Go:          $(go version 2>/dev/null || echo 'n/a')"
echo "  Node:        $(node -v 2>/dev/null || echo 'n/a')"
echo "  运行次数:    $RUNS (对比表每项取中位数)"
echo "==================================================================="

if [[ "$DO_COMPARE" == "1" ]]; then
  echo
  echo ">>> [1/2] 解释器算力对比 (gox / node / goja)"
  "$ROOT/scripts/bench-compare.sh" --runs "$RUNS"
fi

if [[ "$DO_PERF" == "1" ]]; then
  echo
  echo ">>> [2/2] UI 帧率 / GC 毛刺基准 (go test ./perf/)"
  (cd "$ROOT" && go test ./perf/ -v)
fi

echo
echo "全部完成。结果目录: $ROOT/bench-results/"
