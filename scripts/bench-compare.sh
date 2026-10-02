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
#   goja  纯解释器对照系 (bench/goja 独立 module) —— gox 是解释器, 与 V8 比
#         差的是"解释器 vs JIT"的固有差距; goja 同为纯解释器, 才是同类基线。
#         首次需要联网拉依赖: cd bench/goja && go mod download; 构建失败自动跳过。
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

# 本机 PATH 里的 grep 可能是坏的 (静默返回空); 优先用系统绝对路径。
GREP=/usr/bin/grep
[[ -x "$GREP" ]] || GREP=$(command -v grep)

while [[ $# -gt 0 ]]; do
  case "$1" in
    --runs) RUNS="$2"; shift 2 ;;
    --out)  OUT="$2"; shift 2 ;;
    -h|--help) "$GREP" '^#' "$0" | sed 's/^# \{0,1\}//' | head -40; exit 0 ;;
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

# run_rss_kb <cmd...> : 常驻内存峰值 (KB)。
# 兼容两种 time: GNU `time -v` 报 "Maximum resident set size (kbytes)" (KB);
# BSD/macOS `time -l` 报 "maximum resident set size" (bytes)。都没有时退 0。
run_rss_kb() {
  local out
  if [[ -x /usr/bin/time ]]; then
    if /usr/bin/time -v true >/dev/null 2>&1; then
      out=$(/usr/bin/time -v "$@" 2>&1 >/dev/null | "$GREP" "Maximum resident" | "$GREP" -oE '[0-9]+' | tail -1)
      if [[ -n "$out" ]]; then echo "$out"; return; fi
    fi
    if /usr/bin/time -l true >/dev/null 2>&1; then
      out=$(/usr/bin/time -l "$@" 2>&1 >/dev/null | "$GREP" "maximum resident set size" | "$GREP" -oE '[0-9]+' | tail -1)
      if [[ -n "$out" ]]; then echo $(( out / 1024 )); return; fi
    fi
  fi
  echo 0
}

# median <n...>
median() { printf '%s\n' "$@" | sort -n | awk '{a[NR]=$1} END {print (NR%2) ? a[(NR+1)/2] : int((a[NR/2]+a[NR/2+1])/2)}'; }

# measure <stack> <runner-prefix...> : 对三个脚本各跑 RUNS 次, 输出 CSV 行
measure() {
  local stack="$1"; shift
  local s=() f=() t=() r=() v i
  # 预热: 先把三个脚本各空跑一遍, 让二进制/依赖页缓存热起来, 消除"首个进程
  # 冷启动"对中位数的偏置 (尤其 node 的冷启动波动很大)。
  for i in 1 2; do
    "$@" "$BENCH_DIR/startup_idle.js" >/dev/null 2>&1 || true
    "$@" "$BENCH_DIR/fib28.js" >/dev/null 2>&1 || true
    "$@" "$BENCH_DIR/timers_10k.js" >/dev/null 2>&1 || true
  done
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

# goja: 纯解释器对照系 (独立 module)。构建失败 (如离线且依赖未下载) 自动跳过,
# 不影响 gox/node 部分 —— 这正是"不联网也能跑"的要求。
GOJA_BIN=""
if [[ -f "$ROOT/bench/goja/go.mod" ]]; then
  echo "[harness] 构建 goja runner (bench/goja 独立 module) ..." >&2
  if (cd "$ROOT/bench/goja" && go build -o "$OUT/bench-goja" .) 2>"$OUT/.goja-build-err"; then
    GOJA_BIN="$OUT/bench-goja"
  else
    echo "[harness] goja runner 构建失败, 跳过 goja 栈。" >&2
    echo "[harness] 联网后执行: cd bench/goja && go mod download" >&2
    sed 's/^/    /' "$OUT/.goja-build-err" >&2 || true
  fi
fi
if [[ -n "$GOJA_BIN" ]]; then
  echo "[harness] 测量 goja (纯解释器, 与 gox 同类) ..." >&2
  ROWS="$ROWS$(measure goja "$GOJA_BIN")"$'\n'
else
  echo "[harness] 无 goja runner, 跳过" >&2
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
import sys, json, datetime, os, platform, subprocess
rows = []
for line in """$ROWS""".strip().splitlines():
    if not line.strip():
        continue
    stack, startup, fib, timers, rss = line.split(",")
    rows.append({"stack": stack, "startup_ms": int(startup), "fib28_ms": int(fib),
                 "timers10k_ms": int(timers), "rss_kb": int(rss)})

def sh(cmd):
    try:
        return subprocess.check_output(cmd, shell=True, text=True, stderr=subprocess.DEVNULL).strip()
    except Exception:
        return ""

cpu = sh("sysctl -n machdep.cpu.brand_string")
if not cpu:
    try:
        with open("/proc/cpuinfo") as f:
            for ln in f:
                if ln.startswith("model name"):
                    cpu = ln.split(":", 1)[1].strip(); break
    except Exception:
        pass
host = {
    "platform": platform.platform(),
    "machine": platform.machine(),
    "cpu": cpu,
    "cores": os.cpu_count(),
    "python": platform.python_version(),
}
doc = {
    "generated_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
    "runs_per_metric": $RUNS,
    "method": "每项外部 wall time 跑 $RUNS 次取中位数; RSS = 进程常驻峰值 (GNU time VmHWM / BSD time maxrss); 脚本本体零依赖",
    "host": host,
    "stacks": [r["stack"] for r in rows],
    "note": "gox/node/goja 三列同源脚本; goja 是纯解释器对照系 (与 gox 同类), node 是 V8(JIT) 对照; node 列是 Electron 主进程同构口径, 非完整 GUI 空壳",
    "results": rows,
}
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
