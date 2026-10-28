#!/usr/bin/env bash
# ============================================================================
# binary-size.sh — 单文件产物体积 + 自解压启动开销基准 (任务 T3-6)
#
#   bash scripts/binary-size.sh             # 每个组合 25 次, 取中位数
#   RUNS=11 bash scripts/binary-size.sh     # 少跑几次
#   KEEP=1 bash scripts/binary-size.sh      # 保留中间产物目录（排查用）
#
# 为什么要有这个脚本:
#   "单文件分发"是 Gox 的卖点, 但卖点有两个隐含承诺 —— 产物别太大、启动别被
#   自解压拖慢。两者都只能用真实构建 + 真实计时来回答, 不能靠感觉。这个脚本
#   把口径固定下来, 改一行代码后一条命令就能看是赚了还是赔了。
#
# 量什么（全部是外部可观测值, 不读进程内部计数器）:
#   1. 裸 gox 二进制: 默认 / -s -w 各一次（体积 + gzip 后体积）
#   2. 单文件产物: packager/jsbuild 生成的内嵌最小 JS 的 linux/amd64 可执行
#   3. 启动 wall time: 单文件 vs 裸二进制跑同一脚本（中位数 + 最小值/最大值）
#   4. 1 MiB 脚本变体: 体积涨 1 MB 时自解压+词法扫描的边际开销
#   5. 链接方式（static/dynamic）与 CGO_ENABLED=0 静态变体的体积
#
# 产物: bench-results/binary-size-<UTC>.json, 同时把 Markdown 表打到 stdout。
# 失败策略: 某一步构建不出来 → 记进 skipped[] 并继续跑其余步骤, 绝不填假数字。
# ============================================================================
set -euo pipefail
cd "$(dirname "$0")/.."

RUNS=${RUNS:-25}
KEEP=${KEEP:-0}
WARMUP=3 # 预热次数: 头几次会吃缺页/动态链接器冷启动, 不计入结果

TS=$(date -u +%Y%m%dT%H%M%SZ)
mkdir -p bench-results
OUT="bench-results/binary-size-${TS}.json"

WORK=$(mktemp -d "${TMPDIR:-/tmp}/gox-binbench-XXXXXX")
if [[ "$KEEP" == "1" ]]; then
    echo ">>> 中间产物保留在 $WORK"
else
    trap 'rm -rf "$WORK"' EXIT
fi

# 计时只认 bash 5 的 EPOCHREALTIME: 它是内建变量, 读它不 fork, 而 $(date)
# 每次会多 fork 两个进程 —— 而我们要测的正是几毫秒级的进程启动,
# harness 自身开销必须在噪声以下。
if [[ -z "${EPOCHREALTIME:-}" ]]; then
    echo "错误: 需要 bash >= 5 (EPOCHREALTIME), 当前 ${BASH_VERSION}" >&2
    exit 1
fi

SKIPS=()
skip() { SKIPS+=("$1"); echo "!! 跳过: $1" >&2; }

fsize() { stat -c %s "$1" 2>/dev/null || stat -f %z "$1"; }
gzipped() { gzip -9 -c "$1" | wc -c | tr -d ' '; }

us_of() { # 把 EPOCHREALTIME 字符串转微秒; 小数部分补 10# 防八进制陷阱
    local t=$1
    echo $(( ${t%.*} * 1000000 + 10#${t#*.} ))
}

# ---------------------------------------------------------------- 1. 最小应用
# 脚本刻意最小: 只做一次循环求和 + 一行输出。启动时间的主体是引擎自身
# （词法/语法/编译 + VM 初始化）, 脚本越干净, 引擎开销越不被业务代码淹没。
APP="$WORK/app"
mkdir -p "$APP"
cat >"$APP/main.js" <<'JS'
const xs = [1, 2, 3];
let s = 0;
for (const x of xs) s += x;
console.log("gox-bench", s);
JS
SCRIPT_BYTES=$(fsize "$APP/main.js")

# 1 MiB 脚本变体: 用注释填充（注释同样要解压落盘 + 过一遍词法扫描, 但不会
# 引入额外执行时间）, 用来观察"体积涨、启动跟不跟"。
BIG="$WORK/bigapp"
mkdir -p "$BIG"
{
    echo "// pad: 1 MiB 注释, 用于观察体积→启动的边际开销"
    printf -v pad '%98s' ''
    pad=${pad// /p} # 纯 bash 字符串展开, 省掉 1 万次外部命令
    for _ in $(seq 1 10485); do
        echo "//$pad"
    done
    echo 'console.log("gox-bench-big");'
} >"$BIG/main.js"
BIG_SCRIPT_BYTES=$(fsize "$BIG/main.js")

# ------------------------------------------------------------- 2. 裸 gox 二进制
BARE_DEFAULT="$WORK/gox-default"
BARE_SW="$WORK/gox-sw"
BARE_DEFAULT_BYTES=""
BARE_SW_BYTES=""
BARE_DEFAULT_GZ=""
BARE_SW_GZ=""
if go build -o "$BARE_DEFAULT" ./cmd/gox 2>"$WORK/bare-default.log"; then
    BARE_DEFAULT_BYTES=$(fsize "$BARE_DEFAULT")
    BARE_DEFAULT_GZ=$(gzipped "$BARE_DEFAULT")
else
    skip "裸 gox（默认 ldflags）构建失败: $(tail -3 "$WORK/bare-default.log" | tr '\n' ' ')"
fi
if go build -ldflags="-s -w" -o "$BARE_SW" ./cmd/gox 2>"$WORK/bare-sw.log"; then
    BARE_SW_BYTES=$(fsize "$BARE_SW")
    BARE_SW_GZ=$(gzipped "$BARE_SW")
else
    skip "裸 gox（-s -w）构建失败: $(tail -3 "$WORK/bare-sw.log" | tr '\n' ' ')"
fi

# ------------------------------------------------------ 3. 单文件产物 (linux/amd64)
SF="$WORK/app-linux-amd64"
SF_BYTES=""
SF_GZ=""
# 先把 packager 编成二进制再跑: `go run` 每次都要做一次链接, 会把构建耗时
# 混进脚本总时长; 而且 packager 用 runtime.Caller 定位仓库根, 编译期路径
# 才是我们想要的（仓库内的绝对路径）。
JSBUILD="$WORK/jsbuild"
if ! go build -o "$JSBUILD" ./packager 2>"$WORK/jsbuild-build.log"; then
    skip "packager 构建失败: $(tail -3 "$WORK/jsbuild-build.log" | tr '\n' ' ')"
elif ! "$JSBUILD" "$APP/main.js" --target linux/amd64 -o "$SF" --name gox-bench \
    >"$WORK/jsbuild-run.log" 2>&1; then
    # 常见失败: 生成的临时模块要 `go mod tidy`, 依赖不在本地缓存时会联网失败
    skip "单文件打包失败（jsbuild）: $(tail -3 "$WORK/jsbuild-run.log" | tr '\n' ' ')"
else
    SF_BYTES=$(fsize "$SF")
    SF_GZ=$(gzipped "$SF")
fi

# 链接方式单独记一笔: CGO_ENABLED 继承宿主环境, 默认产物在 glibc 机器上是
# 动态链接的（net 包的 cgo 解析器）, 这会影响"单文件能不能直接扔到别的发行版"。
LINKAGE=$(file -b "$SF" 2>/dev/null | grep -o 'statically linked\|dynamically linked' || true)
LINKAGE=${LINKAGE:-unknown}

# CGO_ENABLED=0 变体: jsbuild 内部那次 go build 会继承环境变量, 所以在这里
# 加前缀即可得到静态产物（同一模板、同一脚本, 只有链接方式不同）。
SFSTATIC="$WORK/app-linux-amd64-static"
SFSTATIC_BYTES=""
SFSTATIC_GZ=""
SFSTATIC_LINKAGE=""
if [[ -n "$SF_BYTES" ]]; then
    if ! CGO_ENABLED=0 "$JSBUILD" "$APP/main.js" --target linux/amd64 -o "$SFSTATIC" \
        --name gox-bench >"$WORK/jsbuild-static.log" 2>&1; then
        skip "静态变体打包失败（CGO_ENABLED=0）: $(tail -3 "$WORK/jsbuild-static.log" | tr '\n' ' ')"
    else
        SFSTATIC_BYTES=$(fsize "$SFSTATIC")
        SFSTATIC_GZ=$(gzipped "$SFSTATIC")
        SFSTATIC_LINKAGE=$(file -b "$SFSTATIC" | grep -o 'statically linked\|dynamically linked' || true)
        SFSTATIC_LINKAGE=${SFSTATIC_LINKAGE:-unknown}
    fi
fi

SFBIG="$WORK/app-big-linux-amd64"
SFBIG_BYTES=""
SFBIG_GZ=""
if [[ -n "$SF_BYTES" ]]; then
    if ! "$JSBUILD" "$BIG/main.js" --target linux/amd64 -o "$SFBIG" --name gox-bench-big \
        >"$WORK/jsbuild-big.log" 2>&1; then
        skip "1 MiB 脚本变体打包失败: $(tail -3 "$WORK/jsbuild-big.log" | tr '\n' ' ')"
    else
        SFBIG_BYTES=$(fsize "$SFBIG")
        SFBIG_GZ=$(gzipped "$SFBIG")
    fi
fi

# ------------------------------------------------------------------ 4. 启动计时
# 口径: 外部 wall time, 进程从 fork 到退出（含动态链接、Go runtime 初始化、
# 自解压落盘、词法/语法/编译、执行脚本、退出）。stdout 丢弃（写管道也会计时）。
# 交替采样: A/B/A/B... 而不是 A 全跑完再跑 B —— 机器负载随时间漂移时,
# 交替采样能把漂移摊到两边, 差值的偏差小于分块采样。
declare -a T_SF=() T_BARE=() T_SFBIG=()

run_us() { # $@ = 命令行; 回显单次耗时（微秒）
    local t0 t1
    t0=$EPOCHREALTIME
    "$@" >/dev/null 2>&1 || true
    t1=$EPOCHREALTIME
    echo $(( $(us_of "$t1") - $(us_of "$t0") ))
}

median_of() { # 读 stdin 一行一个整数 → 中位数（偶数取中间两个的均值, 向下取整）
    sort -n | awk '{a[NR]=$1} END{
        if (NR==0) {print ""; exit}
        if (NR%2) print a[(NR+1)/2]
        else print int((a[NR/2]+a[NR/2+1])/2)
    }'
}

# 预热: 两个产物都先跑几次, 让文件页进 page cache（真实分发场景下二进制
# 二次启动就是热的, 冷启动另见 docs 的口径说明）
if [[ -n "$SF_BYTES" ]]; then for _ in $(seq 1 $WARMUP); do "$SF" >/dev/null 2>&1 || true; done; fi
if [[ -n "$BARE_SW_BYTES" ]]; then for _ in $(seq 1 $WARMUP); do "$BARE_SW" "$APP/main.js" >/dev/null 2>&1 || true; done; fi
if [[ -n "$SFBIG_BYTES" ]]; then for _ in $(seq 1 $WARMUP); do "$SFBIG" >/dev/null 2>&1 || true; done; fi

TIMED=0
for _ in $(seq 1 "$RUNS"); do
    if [[ -n "$SF_BYTES" ]]; then T_SF+=("$(run_us "$SF")"); TIMED=1; fi
    if [[ -n "$BARE_SW_BYTES" ]]; then T_BARE+=("$(run_us "$BARE_SW" "$APP/main.js")"); TIMED=1; fi
    if [[ -n "$SFBIG_BYTES" ]]; then T_SFBIG+=("$(run_us "$SFBIG")"); TIMED=1; fi
done
if [[ "$TIMED" == "0" ]]; then
    skip "启动计时: 两个产物都没构建出来, 无法比较"
fi

stat_of() { # $@ = 微秒数组 → "median min max"（整数微秒）
    if [[ $# -eq 0 ]]; then echo "- - -"; return; fi
    local med min max
    med=$(printf '%s\n' "$@" | median_of)
    min=$(printf '%s\n' "$@" | sort -n | head -1)
    max=$(printf '%s\n' "$@" | sort -n | tail -1)
    echo "$med $min $max"
}

read -r SF_MED SF_MIN SF_MAX <<<"$([[ ${#T_SF[@]} -gt 0 ]] && stat_of "${T_SF[@]}" || echo '- - -')"
read -r BARE_MED BARE_MIN BARE_MAX <<<"$([[ ${#T_BARE[@]} -gt 0 ]] && stat_of "${T_BARE[@]}" || echo '- - -')"
read -r SFBIG_MED SFBIG_MIN SFBIG_MAX <<<"$([[ ${#T_SFBIG[@]} -gt 0 ]] && stat_of "${T_SFBIG[@]}" || echo '- - -')"

# 自解压净开销 = 单文件 - 裸二进制。可为负: 裸 gox 二进制带 GUI/子命令等
# 更多包, 初始化更重, 完全可能比"更瘦的单文件产物"还慢 —— 如实记下。
DELTA=""
if [[ "$SF_MED" != "-" && "$BARE_MED" != "-" ]]; then
    DELTA=$(( SF_MED - BARE_MED ))
fi
BIG_DELTA=""
if [[ "$SFBIG_MED" != "-" && "$SF_MED" != "-" ]]; then
    BIG_DELTA=$(( SFBIG_MED - SF_MED )) # 每 1 MiB 脚本的边际开销
fi

# ------------------------------------------------------------------ 5. 落盘 JSON
GO_VER=$(go version | awk '{print $3}')
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
COMMIT_SUBJ=$(git log -1 --pretty=%s 2>/dev/null | sed 's/"/\\"/g' || echo "unknown")
CPU=$(grep -m1 'model name' /proc/cpuinfo 2>/dev/null | cut -d: -f2 | xargs || echo "unknown")
CORES=$(nproc 2>/dev/null || echo 0)
UNAME=$(uname -srm)

js() { # null-safe 数字输出
    if [[ -z "${1:-}" ]]; then echo null; else echo "$1"; fi
}
json_arr() {
    if [[ $# -eq 0 ]]; then echo "[]"; return; fi
    local out="" s
    for s in "$@"; do
        s=${s//\\/\\\\}; s=${s//\"/\\\"}
        out+="${out:+, }\"$s\""
    done
    echo "[$out]"
}

SKIPPED_JSON=$([[ ${#SKIPS[@]} -eq 0 ]] && echo "[]" || json_arr "${SKIPS[@]}")

cat >"$OUT" <<JSON
{
  "generated_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "runs": $RUNS,
  "warmup_runs": $WARMUP,
  "method": "外部 wall time（bash EPOCHREALTIME, 微秒）; 交替采样 A/B; stdout 丢弃; 中位数 + 最小/最大; 预热 $WARMUP 次（page cache 热）",
  "toolchain": {
    "go": "$GO_VER",
    "goos": "$(go env GOOS)",
    "goarch": "$(go env GOARCH)",
    "commit": "$COMMIT",
    "commit_subject": "$COMMIT_SUBJ"
  },
  "host": {
    "uname": "$UNAME",
    "cpu": "$CPU",
    "cores": $CORES
  },
  "target": "linux/amd64",
  "minimal_script_bytes": $SCRIPT_BYTES,
  "big_script_bytes": $BIG_SCRIPT_BYTES,
  "sizes_bytes": {
    "gox_bare_default": { "raw": $(js "$BARE_DEFAULT_BYTES"), "gzip": $(js "$BARE_DEFAULT_GZ") },
    "gox_bare_s_w": { "raw": $(js "$BARE_SW_BYTES"), "gzip": $(js "$BARE_SW_GZ") },
    "single_file": { "raw": $(js "$SF_BYTES"), "gzip": $(js "$SF_GZ"), "linkage": "$LINKAGE" },
    "single_file_static": { "raw": $(js "$SFSTATIC_BYTES"), "gzip": $(js "$SFSTATIC_GZ"), "linkage": "$SFSTATIC_LINKAGE" },
    "single_file_1mib_script": { "raw": $(js "$SFBIG_BYTES"), "gzip": $(js "$SFBIG_GZ") }
  },
  "startup_us": {
    "single_file": { "median": $(js "${SF_MED/-/}"), "min": $(js "${SF_MIN/-/}"), "max": $(js "${SF_MAX/-/}") },
    "gox_bare_s_w": { "median": $(js "${BARE_MED/-/}"), "min": $(js "${BARE_MIN/-/}"), "max": $(js "${BARE_MAX/-/}") },
    "single_file_1mib_script": { "median": $(js "${SFBIG_MED/-/}"), "min": $(js "${SFBIG_MIN/-/}"), "max": $(js "${SFBIG_MAX/-/}") }
  },
  "delta_us": {
    "single_vs_bare": $(js "$DELTA"),
    "per_1mib_script": $(js "$BIG_DELTA")
  },
  "skipped": $SKIPPED_JSON
}
JSON

# ------------------------------------------------------------ 6. 人眼可读输出
is_missing() { [[ -z "${1:-}" || "$1" == "-" ]]; } # 构建被跳过时占位符是 "-"
fmt_mb() { if is_missing "$1"; then echo "-"; else awk -v b="$1" 'BEGIN{printf "%.2f", b/1000000}'; fi; }
fmt_ms() { if is_missing "$1"; then echo "-"; else awk -v u="$1" 'BEGIN{printf "%.2f", u/1000}'; fi; }
pct() { # 相对变化（$1 相对 $2）
    if is_missing "$1" || is_missing "$2" || [[ "$2" == "0" ]]; then echo "-"; return; fi
    awk -v a="$1" -v b="$2" 'BEGIN{printf "%+.1f%%", (a-b)/b*100}'
}

echo
echo "### 体积（linux/amd64, 单位 MB = 10^6 B）"
echo
echo "| 产物 | 原始 | gzip -9 | 相对裸 -s -w |"
echo "| --- | ---: | ---: | ---: |"
echo "| 裸 gox（默认 ldflags） | $(fmt_mb "$BARE_DEFAULT_BYTES") | $(fmt_mb "$BARE_DEFAULT_GZ") | $(pct "$BARE_DEFAULT_BYTES" "$BARE_SW_BYTES") |"
echo "| 裸 gox（-s -w） | $(fmt_mb "$BARE_SW_BYTES") | $(fmt_mb "$BARE_SW_GZ") | 基线 |"
echo "| 单文件（最小脚本 ${SCRIPT_BYTES} B, ${LINKAGE}） | $(fmt_mb "$SF_BYTES") | $(fmt_mb "$SF_GZ") | $(pct "$SF_BYTES" "$BARE_SW_BYTES") |"
echo "| 单文件（CGO_ENABLED=0, ${SFSTATIC_LINKAGE:-unknown}） | $(fmt_mb "$SFSTATIC_BYTES") | $(fmt_mb "$SFSTATIC_GZ") | $(pct "$SFSTATIC_BYTES" "$BARE_SW_BYTES") |"
echo "| 单文件（1 MiB 脚本） | $(fmt_mb "$SFBIG_BYTES") | $(fmt_mb "$SFBIG_GZ") | $(pct "$SFBIG_BYTES" "$BARE_SW_BYTES") |"
echo
echo "### 启动 wall time（${RUNS} 次, ms）"
echo
echo "| 组合 | 中位数 | 最小 | 最大 | 相对裸二进制 |"
echo "| --- | ---: | ---: | ---: | ---: |"
echo "| 裸 gox -s -w + 脚本 | $(fmt_ms "$BARE_MED") | $(fmt_ms "$BARE_MIN") | $(fmt_ms "$BARE_MAX") | 基线 |"
echo "| 单文件 | $(fmt_ms "$SF_MED") | $(fmt_ms "$SF_MIN") | $(fmt_ms "$SF_MAX") | $(pct "$SF_MED" "$BARE_MED") |"
echo "| 单文件（1 MiB 脚本） | $(fmt_ms "$SFBIG_MED") | $(fmt_ms "$SFBIG_MIN") | $(fmt_ms "$SFBIG_MAX") | $(pct "$SFBIG_MED" "$BARE_MED") |"
echo
if [[ -n "$DELTA" ]]; then
    echo "自解压净开销（单文件 - 裸二进制）: $(fmt_ms "$DELTA") ms"
fi
if [[ -n "$BIG_DELTA" ]]; then
    echo "每 1 MiB 脚本的边际开销: $(fmt_ms "$BIG_DELTA") ms（含解压落盘 + 词法扫描, 未拆分）"
fi
if [[ ${#SKIPS[@]} -gt 0 ]]; then
    echo
    echo "跳过项:"
    printf '  - %s\n' "${SKIPS[@]}"
fi
echo
echo "JSON: $OUT"
