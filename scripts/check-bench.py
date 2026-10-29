#!/usr/bin/env python3
"""性能基准回归闸门 —— 与 bench-compare.sh 配套（看板 rKpKpD）。

背景：Gox 的 bench 工具链是齐的（scripts/bench-all.sh、bench-compare.sh、
scripts/bench/*.js），结果也一直存档在 bench-results/，但**没有任何一条闸门
读它们** —— 性能退化只能靠人记得手动跑、手动比数字。最近 test262 冲刺大量动了
vm / compiler 的热路径，这正是最容易"悄悄引入每帧/每次调用成本上升"的时候。

## 为什么不能直接拿绝对毫秒跟历史比

历史 JSON 是在开发机上跑的，CI 是云上 runner，两台机器的 CPU、负载、页缓存状态
都不同。**跨机器比绝对时间必然误报** —— 一个永远红的闸门比没有闸门更糟（它会被
人当成噪声关掉）。所以本脚本判两类东西：

1. **比值判据（主，跨平台）**：同一份结果里 `gox / node` 的比值。两个栈跑的是
   完全同源的脚本（scripts/bench/*.js），机器差异被约掉；gox 自己退化 ⇒ 比值
   上升。这一条对**时间指标**不挑机器，是闸门的主力。
   **rss_kb 是例外（2026-10-29 实测教训）**：「机器差异被约掉」的前提在 RSS 上
   不成立 —— JS 栈的 RSS 受 runner 内存规格 / node 构建 / 内核版本影响，跨机型
   漂移可达 ±30%（当晚实测：goja RSS 10432→7304 KB，而 gox 自己的 RSS 反而降了
   3.9%），远超 1.15 阈值带宽 —— 比值虚红纯属对照栈漂移，不是引擎退化。所以
   rss 的比值只在 host 指纹与基线一致时判红，跨机型降级为「只记录」（与下面
   绝对值判据的同机型保护对齐）。
2. **绝对值判据（辅，仅限同机型）**：只有当 host 指纹（platform / machine /
   cpu / cores）与基线**完全一致**时才启用；不一致时该项降级为「只记录」，
   并在输出里写明原因 —— 不拿一个注定误报的判据去刷红。

## 噪声过滤

光看比值还不够：startup_ms 从 3ms 涨到 4ms 是 1.33×，但它毫无意义。所以每条
指标还带一个 `MIN_ABS`（绝对增量下限），现值与基线之差没超过它就**不判红**，
只记录。阈值分档 + 最小增量，两头一起掐，才能既抓真退化又不噪声性红。

## 用法

    python3 scripts/check-bench.py                      # 比最新的 bench-*.json
    python3 scripts/check-bench.py --json <file>        # 比指定结果
    python3 scripts/check-bench.py --update-baseline    # 把当前结果写成新基线
    python3 scripts/check-bench.py --github             # 失败时额外输出 ::error::
    python3 scripts/check-bench.py --self-test          # 负向自测（闸门自检）

退出码：0 = 通过（或只有记录项）；1 = 存在回归；2 = 用法/数据错误。
"""
from __future__ import annotations

import glob
import json
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
RESULTS = os.path.join(ROOT, "bench-results")
BASELINE = os.path.join(RESULTS, "baseline.json")

METRICS = ["startup_ms", "fib28_ms", "timers10k_ms", "rss_kb"]

# 分档阈值：指标 → 允许的最大倍数（现值 / 基线，或比值 / 基线比值）。
#   上限之所以分开写：fib28 是纯算力，对引擎热路径改动最敏感，卡 1.20；
#   timers10k 走调度器，稍松；startup 只有几十毫秒，机器噪声占比最大，卡 1.50
#   （它主要靠比值看趋势，不是靠这一条判红）；RSS 涨 15% 就要看一眼。
MAX_RATIO = {
    "startup_ms": 1.50,
    "fib28_ms": 1.20,
    "timers10k_ms": 1.25,
    "rss_kb": 1.15,
}

# 最小绝对增量：低于这个差值就当噪声，不判红（单位与指标一致，ms / KB）。
MIN_ABS = {
    "startup_ms": 10,
    "fib28_ms": 20,
    "timers10k_ms": 20,
    "rss_kb": 2048,
}

# 参与比值判据的对照栈（[T2-3]）。node 是 CI 镜像里必有的，且与 gox 跑同源
# 脚本；bun（JavaScriptCore）由 bench.yml 的 setup-bun 装进 runner，与 node
# 完全同口径 —— 引入它的意义：V8(JIT) 与 JSC 两个引擎对照系互为备份，某天
# node 换版本或口径漂移时，bun 仍能钉住 gox 的相对位置。判定规则：**每个有
# 数据的对照栈都判**（基线里记了谁、本次结果里又有谁，谁就参与）。
# goja 需要联网拉依赖（bench/goja 独立 module），有则更好、无则不强求。
# 缺栈不红：某栈本次没跑出来 → 只记录，不影响判定（node 必在，兜底成立）。
REFERENCE_STACKS = ["node", "bun", "goja"]


def die(msg):
    print("bench-gate: %s" % msg)
    sys.exit(2)


def latest_result():
    files = sorted(glob.glob(os.path.join(RESULTS, "bench-*.json")))
    files = [f for f in files if not f.endswith("baseline.json")]
    if not files:
        die("bench-results/ 下没有 bench-*.json —— 先跑 bash scripts/bench-compare.sh")
    return files[-1]


def load(path):
    try:
        with open(path, encoding="utf-8") as f:
            return json.load(f)
    except (OSError, ValueError) as e:
        die("读不了 %s：%s" % (path, e))


def as_map(doc):
    """{栈名: {指标: 值}}。缺指标记 None（后面按"该项不参与判定"处理）。"""
    out = {}
    for r in doc.get("results", []):
        out[r.get("stack", "?")] = {m: r.get(m) for m in METRICS}
    return out


def host_fingerprint(doc):
    h = doc.get("host", {}) or {}
    return "%s|%s|%s|%s" % (h.get("platform", ""), h.get("machine", ""),
                            h.get("cpu", ""), h.get("cores", ""))


def ratios(cur_map):
    """算 gox 相对**每个**可用对照栈的比值；返回 {指标: [(对照栈, 比值), ...]}。

    [T2-3] 之前只取第一个命中的对照栈；bun 引入后一个结果里可能有多个引擎
    对照，全都记下来，判红时逐个比（哪个对照系发现退化，就在错误信息里点
    哪个的名）。
    """
    gox = cur_map.get("gox", {})
    out = {}
    for metric in METRICS:
        g = gox.get(metric)
        if not g:
            continue
        refs = []
        for ref in REFERENCE_STACKS:
            r = cur_map.get(ref, {}).get(metric)
            if r:
                refs.append((ref, g / float(r)))
        if refs:
            out[metric] = refs
    return out


def build_baseline(doc):
    cur = as_map(doc)
    return {
        "说明": "性能基准基线 —— 由 python3 scripts/check-bench.py --update-baseline "
                "生成。改了引擎热路径且确认是有意的性能变化后重建它；"
                "平时 CI 只比对、不自动刷新（自动刷新等于把退化藏起来）。",
        "生成方式": "python3 scripts/check-bench.py --update-baseline",
        "源结果": (os.path.relpath(doc["__path__"], ROOT)
                   if doc.get("__path__") else "（自测合成数据）"),
        "生成时间": doc.get("generated_at", ""),
        "host": doc.get("host", {}),
        "runs_per_metric": doc.get("runs_per_metric"),
        "指标": {k: v for k, v in cur.items()},
        # 新格式：{指标: {对照栈: 比值}}，bun 加入后一个指标可对应多个对照栈。
        # 旧格式（{指标: {"ref": ..., "value": ...}}）在 compare() 里仍兼容。
        "比值": {m: {ref: v for ref, v in refs} for m, refs in ratios(cur).items()},
    }


def base_ratios_of(base_ratio):
    """把基线的 比值 字段统一成 {指标: {对照栈: 比值}}。

    兼容两种格式：旧版（bun 引入前）是 {指标: {"ref": str, "value": float}}，
    新版是 {指标: {对照栈: 比值}}。旧基线不重建也能继续用。
    """
    out = {}
    for metric, v in (base_ratio or {}).items():
        if isinstance(v, dict) and "ref" in v and "value" in v:
            out[metric] = {v["ref"]: v["value"]}
        elif isinstance(v, dict):
            out[metric] = dict(v)
    return out


def compare(base, doc, github=False):
    """返回 (回归列表, 记录列表, 表行)。"""
    cur = as_map(doc)
    base_map = base.get("指标", {})
    base_ratio = base_ratios_of(base.get("比值", {}))
    cur_ratio = ratios(cur)

    same_host = host_fingerprint(base) == host_fingerprint(doc)
    regressions, notes, rows = [], [], []

    for metric in METRICS:
        g = cur.get("gox", {}).get(metric)
        b = (base_map.get("gox") or {}).get(metric)
        if not g:
            notes.append("gox 缺指标 %s（本次结果里没有它），跳过" % metric)
            continue

        # ① 比值判据（跨平台，主力）——对**每个**基线里有、本次又有的对照栈判。
        #    rss_kb 例外：跨机型时只记录不判红（见文件头「rss_kb 是例外」）。
        if metric in cur_ratio and metric in base_ratio:
            base_refs = base_ratio[metric]
            cur_refs = dict(cur_ratio[metric])
            if metric == "rss_kb" and not same_host:
                for ref in REFERENCE_STACKS:
                    cr = cur_refs.get(ref)
                    br = base_refs.get(ref)
                    if cr and br:
                        rows.append(("gox/%s %s" % (ref, metric), br, cr,
                                     cr / float(br)))
                notes.append(
                    "%s：host 与基线不一致（%s vs %s），对照栈 RSS 跨机型漂移大"
                    "（实测可达 ±30%%），比值只记录不判红"
                    % (metric, host_fingerprint(doc), host_fingerprint(base)))
                continue
            judged = False
            for ref in REFERENCE_STACKS:
                cr = cur_refs.get(ref)
                br = base_refs.get(ref)
                if not cr or not br:
                    continue
                judged = True
                r = cr / float(br)
                rows.append(("gox/%s %s" % (ref, metric), br, cr, r))
                if r > MAX_RATIO[metric]:
                    if metric == "rss_kb":
                        why = ("内存比升高 —— gox 涨内存或对照栈缩水，"
                               "先看两栈各自的绝对值再归因")
                    else:
                        why = ("引擎相对 %s 变慢了，通常是热路径的每次调用成本上升"
                               % ref)
                    regressions.append(
                        "%s 的 gox/%s 比值从 %.3f 涨到 %.3f（×%.2f，阈值 %.2f）—— %s"
                        % (metric, ref, br, cr, r, MAX_RATIO[metric], why))
            if judged:
                continue
            # 比值字段两边都有，但对照栈对不上（如基线只记了 node、本次只跑了
            # bun）：显式说明为什么没判，不留黑箱。
            notes.append("%s：基线对照栈 %s 与本次 %s 无交集，比值判据本轮不判，只记录"
                         % (metric, sorted(base_refs), [r for r, _ in cur_ratio[metric]]))

        # ② 绝对值判据（仅同机型）
        if not b:
            notes.append("%s：基线里没有 gox 的绝对值，跳过" % metric)
            continue
        r = g / float(b)
        rows.append(("gox %s" % metric, b, g, r))
        if not same_host:
            notes.append("%s：host 与基线不一致（%s vs %s），绝对值只记录不判红"
                         % (metric, host_fingerprint(doc), host_fingerprint(base)))
            continue
        if r <= MAX_RATIO[metric]:
            continue
        if g - b < MIN_ABS[metric]:
            notes.append("%s：比值 %.2f 超阈值，但只涨了 %d（< 最小增量 %d），"
                         "按噪声处理" % (metric, r, g - b, MIN_ABS[metric]))
            continue
        regressions.append(
            "gox 的 %s 从 %d 涨到 %d（×%.2f，阈值 %.2f，且涨幅 %d 超过最小增量 %d）"
            % (metric, b, g, r, MAX_RATIO[metric], g - b, MIN_ABS[metric]))
    return regressions, notes, rows


def fmt_rows(rows):
    if not rows:
        return []
    out = ["  %-28s %14s %14s %8s" % ("判据", "基线", "现值", "比值")]
    for name, b, g, r in rows:
        out.append("  %-28s %14.3f %14.3f %7.2fx" % (name, b, g, r))
    return out


def main_check(path, github, update):
    doc = load(path)
    doc["__path__"] = path

    if update:
        base = build_baseline(doc)
        with open(BASELINE, "w", encoding="utf-8") as f:
            json.dump(base, f, ensure_ascii=False, indent=2)
            f.write("\n")
        print("已写入基线 %s" % os.path.relpath(BASELINE, ROOT))
        print("  host：%s" % (doc.get("host", {}).get("cpu", "?") or "?")[:60])
        for metric, refs in sorted(ratios(as_map(doc)).items()):
            for ref, v in refs:
                print("  gox/%s %s = %.3f" % (ref, metric, v))
        return 0

    if not os.path.isfile(BASELINE):
        print("!! 没有基线 %s —— 先跑一次：python3 scripts/check-bench.py "
              "--update-baseline" % os.path.relpath(BASELINE, ROOT))
        return 1
    base = load(BASELINE)
    regressions, notes, rows = compare(base, doc)

    print("性能基准闸门：%s" % os.path.relpath(path, ROOT))
    print("  基    线：%s（%s）" % (base.get("生成时间", "?"),
                                    (base.get("host", {}).get("cpu", "?") or "?")[:40]))
    for line in fmt_rows(rows):
        print(line)
    for n in notes:
        print("  · %s" % n)
    print()

    if not regressions:
        print("ok  无性能回归（%d 项判据，%d 条记录）" % (len(rows), len(notes)))
        return 0

    print("FAIL 检测到 %d 项性能回归：" % len(regressions))
    for r in regressions:
        print("  - %s" % r)
    if github:
        flat = " ｜ ".join(regressions)
        print("::error::性能基准回归 —— %s" % flat[:2000])
    return 1


def self_test():
    """负向自测：**一个从没失败过的闸门等于黑盒**。

    这里用合成数据钉住七件事：
      1. gox 相对 node 慢 1.5×  ⇒ 必须红，且点名是哪个指标；
      2. gox 相对 bun 慢（且本次结果里没有 node）⇒ 必须红，点名 bun ——
         [T2-3] bun 成为正式对照系后，node 缺席不能让判据跟着失效；
      3. 内存涨 1.4×            ⇒ 必须红；
      4. 正常波动（1.05×）      ⇒ 必须绿（防"判据过宽/过窄"两个方向）；
      5. 涨幅超比值但低于最小增量 ⇒ 必须绿（噪声过滤真的生效）；
      6. 跨 host 时 rss 比值虚涨 ⇒ 必须绿（对照栈内存跨机型漂移不判红，
         2026-10-29 实测：goja RSS 在另一台 runner 上缩水 30% 把闸门打红）；
      7. 跨 host 时时间指标退化 ⇒ 仍必须红（rss 例外不能外溢到时间指标）。
    """
    def doc(gox, node, bun=None, host="selftest"):
        results = [
            {"stack": "gox", "startup_ms": gox[0], "fib28_ms": gox[1],
             "timers10k_ms": gox[2], "rss_kb": gox[3]},
        ]
        if node is not None:
            results.append({"stack": "node", "startup_ms": node[0],
                            "fib28_ms": node[1], "timers10k_ms": node[2],
                            "rss_kb": node[3]})
        if bun is not None:
            results.append({"stack": "bun", "startup_ms": bun[0],
                            "fib28_ms": bun[1], "timers10k_ms": bun[2],
                            "rss_kb": bun[3]})
        return {"generated_at": "selftest", "runs_per_metric": 1,
                "host": {"platform": host, "machine": host,
                         "cpu": host, "cores": 1},
                "results": results}

    # 基线含 node 与 bun 两个对照栈（新版基线格式：每个对照栈各记一份比值）。
    base_doc = doc([30, 100, 200, 20000], [30, 50, 40, 30000], [30, 60, 50, 35000])
    base = build_baseline(base_doc)

    cases = [
        ("算力退化 1.5× 必须红",
         doc([30, 150, 200, 20000], [30, 50, 40, 30000], [30, 60, 50, 35000]),
         base, True, "fib28_ms"),
        ("node 缺席时 gox/bun 退化必须红（bun 单独也能触发闸门）",
         doc([30, 150, 200, 20000], None, [30, 60, 50, 35000]),
         base, True, "gox/bun"),
        ("内存涨 1.4× 必须红",
         doc([30, 100, 200, 28000], [30, 50, 40, 30000], [30, 60, 50, 35000]),
         base, True, "rss_kb"),
        ("正常波动必须绿",
         doc([31, 104, 205, 20100], [30, 50, 40, 30000], [30, 60, 50, 35000]),
         base, False, ""),
        ("涨幅低于最小增量必须绿（噪声）",
         doc([35, 100, 200, 20000], [60, 100, 80, 60000], [70, 120, 100, 70000]),
         base, False, ""),
        # 2026-10-29 实测案例的还原：gox RSS 反而降了，goja 在另一台 runner 上
        # 缩水 30%，把 gox/goja 内存比从 2.27 顶到 3.11 —— 跨机型时必须只记录。
        ("跨 host 时 rss 比值虚涨必须绿（对照栈内存跨机型漂移）",
         doc([30, 100, 200, 18700], [30, 50, 40, 30000], [30, 60, 50, 35000],
             host="otherrunner"),
         base, False, ""),
        ("跨 host 时时间指标退化仍必须红（rss 例外不外溢）",
         doc([30, 150, 200, 20000], [30, 50, 40, 30000], [30, 60, 50, 35000],
             host="otherrunner"),
         base, True, "fib28_ms"),
    ]
    ok = True
    for name, d, b, want_red, want_kw in cases:
        regressions, _, _ = compare(b, d)
        got_red = bool(regressions)
        if got_red != want_red:
            ok = False
            print("FAIL %s：期望%s，实际%s\n    %s"
                  % (name, "红" if want_red else "绿", "红" if got_red else "绿",
                     "\n    ".join(regressions)))
            continue
        if want_red and want_kw and want_kw not in " ".join(regressions):
            ok = False
            print("FAIL %s：红是红了，但没点名 %s\n    %s"
                  % (name, want_kw, "\n    ".join(regressions)))
            continue
        print("ok  %s" % name)
    return 0 if ok else 1


def main():
    argv = sys.argv[1:]
    if "--self-test" in argv:
        return self_test()

    update = "--update-baseline" in argv
    github = "--github" in argv
    path = None
    if "--json" in argv:
        i = argv.index("--json")
        if i + 1 >= len(argv):
            die("--json 后面要跟文件路径")
        path = argv[i + 1]
    if path is None:
        path = latest_result()
    if not os.path.isfile(path):
        die("找不到结果文件 %s" % path)
    return main_check(path, github, update)


if __name__ == "__main__":
    sys.exit(main())
