#!/usr/bin/env bash
# ============================================================================
# bench-compare.sh — Gox 性能基准 harness (T03 交付)
#
# 一条命令出对比表:
#   ./scripts/bench-compare.sh                 # 全部可用栈
#   ./scripts/bench-compare.sh --runs 5        # 每项跑 5 次取中位数 (默认 3)
#   ./scripts/bench-compare.sh --out mydir     # 结果目录 (默认 bench-results/)
#
# 口径 (全部外部计时, 脚本本体零依赖, Gox 与 Node 同源可比):
#   startup_ms   引擎启动 + 最小脚本求值 (startup_idle.js)
#   fib28_ms     fib(28) 递归 + 10 万次调用 (fib28.js) —— 引擎算力
#   timers10k_ms 1 万次 setTimeout(0) 全部触发 (timers_10k.js)
#   rss_kb       空载常驻内存峰值 (GNU time -v, VmHWM)
#
# 支持的栈 (自动探测, 缺哪个跳哪个):
#   gox   本仓库构建 (核心基线, 必有)
#   node  Node.js 同脚本基线 (Electron 主进程 = Node 运行时, 口径同构;
#         注意这不是 Electron 完整 GUI 空壳的数据)
#   electron  需要环境变量 ELECTRON_BIN 指向 electron 可执行文件 +
#             scripts/bench/electron/ 目录; 无显示环境时请自行加 xvfb-run
#   tauri     需要已构建的空壳二进制 (TAURI_BIN) —— Rust 侧构建一次,
#             复用本 harness 的同一套脚本走 tauri 的 invoke? 不做:
#             Tauri 空壳本身是 webview, 计量口径是"启动+空载内存",
#             详见 scripts/bench/tauri/README.md
#
# 结果: <out>/bench-<UTC时间>.json + stdout 一张 Markdown 表格。
# 历史存档留在 <out>/ 里, git 提交进去供跨版本对比。
# ============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RUNS=3
OUT="$ROOT/bench-results"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --runs) RUNS="$2"; shift 2 ;;
    --out)  OUT="$2"; shift 2 ;;
    -h|--help) grep '^#' "$0" | sed 's/^# \{0,1\}//' | head -40; exit 0 ;;
    *) echo "未知参数 $1 (见 --help)"; exit 1 ;;
  esac
done
mkdir -p "$OUT"

BENCH_DIR="$ROOT/scripts/bench"
command -v python3 >/dev/null || { echo "需要 python3 聚合结果"; exit 1; }

# -------- 计量原语 --------

# run_wall <cmd...> : 打印 wall ms (stderr 只留错误; 失败时原样抛出并终止)
run_wall() {
  local t0 t1
  t0=$(date +%s%N)
  if ! "$@" >/dev/null 2>"$OUT/.last-err"; then
    echo "[harness] 测量目标失败: $*" >&2
    echo "[harness] —— stderr 如下 ——" >&2
    cat "$OUT/.last-err" >&2
    exit 1
  fi
  t1=$(date +%s%N)
  echo $(( (t1 - t0) / 1000000 ))
}

# run_rss_kb <cmd...> : GNU time -v 的 Max RSS (KB); 不可用时退 0
run_rss_kb() {
  if [[ -x /usr/bin/time ]]; then
    /usr/bin/time -v "$@" 2>&1 >/dev/null | grep "Maximum resident" | grep -oE '[0-9]+' | tail -1
  else
    echo 0
  fi
}

# median <n...>
median() { printf '%s\n' "$@" | sort -n | awk '{a[NR]=$1} END {print (NR%2) ? a[(NR+1)/2] : int((a[NR/2]+a[NR/2+1])/2)}'; }

# measure <stack> <runner-prefix...> : 对三个脚本各跑 RUNS 次, 输出 CSV 行
measure() {
  local stack="$1"; shift
  local s=() f=() t=() r=() v i
  for i in $(seq 1 "$RUNS"); do s+=("$(run_wall "$@" "$BENCH_DIR/startup_idle.js")"); done
  for i in $(seq 1 "$RUNS"); do f+=("$(run_wall "$@" "$BENCH_DIR/fib28.js")"); done
  for i in $(seq 1 "$RUNS"); do t+=("$(run_wall "$@" "$BENCH_DIR/timers_10k.js")"); done
  for i in $(seq 1 "$RUNS"); do r+=("$(run_rss_kb "$@" "$BENCH_DIR/startup_idle.js")"); done
  v=$(run_wall "$@" "$BENCH_DIR/startup_idle.js") # warm cache 后再取一次代表值
  echo "$stack,$(median "${s[@]}"),$(median "${f[@]}"),$(median "${t[@]}"),$(median "${r[@]}")"
}

# -------- 汇总栈 --------

ROWS=""
echo "[harness] 构建本仓库 gox ..." >&2
GOX_BIN="$OUT/gox-bench"
(cd "$ROOT" && go build -o "$GOX_BIN" ./cmd/gox) >&2

echo "[harness] 测量 gox ..." >&2
ROWS="$ROWS$(measure gox "$GOX_BIN")"$'\n'

if command -v node >/dev/null; then
  echo "[harness] 测量 node ($(node -v)) ..." >&2
  ROWS="$ROWS$(measure node node)"$'\n'
else
  echo "[harness] 无 node, 跳过" >&2
fi

if [[ -n "${ELECTRON_BIN:-}" ]]; then
  echo "[harness] 测量 electron ..." >&2
  ROWS="$ROWS$(measure electron "$ELECTRON_BIN" "$BENCH_DIR/electron")"$'\n'
else
  echo "[harness] 未设 ELECTRON_BIN, 跳过 electron (GUI 空壳口径见 scripts/bench/tauri/README.md)" >&2
fi

# -------- 出表 + JSON --------

STAMP=$(date -u +%Y%m%d-%H%M%S)
JSON="$OUT/bench-$STAMP.json"
python3 - "$JSON" <<PYEOF
import sys, json, datetime
rows = []
for line in """$ROWS""".strip().splitlines():
    stack, startup, fib, timers, rss = line.split(",")
    rows.append({"stack": stack, "startup_ms": int(startup), "fib28_ms": int(fib),
                 "timers10k_ms": int(timers), "rss_kb": int(rss)})
doc = {"generated_at": datetime.datetime.utcnow().isoformat() + "Z",
       "runs_per_metric": $RUNS, "note": "wall time 中位数; RSS=GNU time VmHWM; node 列是 Electron 主进程同构口径",
       "results": rows}
json.dump(doc, open(sys.argv[1], "w"), ensure_ascii=False, indent=2)
print(json.dumps(doc, ensure_ascii=False, indent=2))
PYEOF

echo
echo "| 栈 | 启动 (ms) | fib28+10万次调用 (ms) | 1万次定时器 (ms) | 空载 RSS (KB) |"
echo "|----|-----------|----------------------|------------------|---------------|"
python3 -c "
import json
doc = json.load(open('$JSON'))
for r in doc['results']:
    print('| %s | %d | %d | %d | %d |' % (r['stack'], r['startup_ms'], r['fib28_ms'], r['timers10k_ms'], r['rss_kb']))
"
echo
echo "结果已存档: $JSON (历史全部在 $OUT/ 下, 建议随仓库提交)"
