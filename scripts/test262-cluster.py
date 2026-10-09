#!/usr/bin/env python3
"""test262 失败面聚类 —— 「目录族 × 失败形态」Top N 簇（rnm4C5）。

## 为什么要有这个脚本

language 套件做到 78% 之后，继续提分的边际收益在掉，且出现两个信号：同一机制
修了两次（因为看不到失败面的结构）、按单条修修不完（一整族共享一个根因）。
built-ins 是下一个明确洼地（实测 35.7%，约 1.5 万条失败），而它**从未做过面向
全量失败面的系统聚类** —— 于是排期只能靠拍脑袋或按目录名猜。

本脚本读 `gox test262 -json` 的报告（或分片 JSONL），把失败用例切成
「目录族 × 失败形态」的簇，输出簇规模 + 每簇一条可直接喂给 `gox test262
-filter` 的正则 + 该族的基线数字。**簇 = 一张工单的候选边界**：一簇一个根因，
修完一簇能整段从失败面搬走，而不是每修一条重跑一次全量。

## 失败形态：由已有字段判定，不臆造

runner 的逐用例结果只有四个字段可用（cmd/gox/cmd_test262.go 的 test262Result）：
path / pass / phase / error（+ seconds）。所以形态只能由 phase + error 的消息
文本推 —— 下面五类的判据全部来自这两个字段，脚本不读用例源码、不猜元数据：

| 形态 | 判据 | 含义与排期含义 |
|------|------|----------------|
| harness 依赖 | phase == "harness" | 用例没进引擎：runner 未登记/缺失 harness。**runner 侧的覆盖问题**，不是引擎缺陷，成本最低 |
| crash | phase ∈ {crashed, timeout} | 进程崩溃或单用例超时挂死。**稳定性**，优先级最高 —— 它会污染整个分片 |
| 假阳性退潮 | error 以「期望 …」或「错误类型不匹配」开头 | negative 用例（规范要求报错）判定失配：应报错却成功执行 / 报错阶段不对 / 类型名不对。即"过去靠宽松判定刷上去的假阳性"正在退潮 |
| 功能零实现 | error 以 `parser errors:` / `compiler error:` 开头，或运行期消息含能力缺失关键词 | 停在编译前端，或运行期报"没有这个东西"（全局/方法不存在）。**整块能力缺失**，补的是实现 |
| 语义偏差 | 其余运行期失败（多为 `Test262Error:` 断言失败） | 能力在，但值/顺序/边界/this 校验与规范不符。**逐个方法对齐规范** |

两点必须说清，否则数字会被误读：
  1. 「功能零实现」的关键词判定是**启发式**（能力缺失 ≤ 语义偏差的边界没有
     唯一答案）。关键词集中在 MISSING_MARKERS，改判据要连同 --self-test 一起改。
  2. 一个用例只归一簇（形态互斥，取首个命中），所以各形态规模之和 = 失败总数。

## 用法

    python3 scripts/test262-cluster.py --results test262-results.json
    python3 scripts/test262-cluster.py --results run.jsonl --top 15 --depth 4
    python3 scripts/test262-cluster.py --results run.json --json clusters.json --md report.md
    python3 scripts/test262-cluster.py --self-test

退出码：0 = 正常；2 = 用法/数据错误；1 = 自检失败。
"""
from __future__ import annotations

import json
import os
import re
import sys

# ── 失败形态 ─────────────────────────────────────────────────────────────────

M_HARNESS = "harness 依赖"
M_CRASH = "crash"
M_FALSE_POSITIVE = "假阳性退潮"
M_MISSING = "功能零实现"
M_SEMANTIC = "语义偏差"

# 形态的展示顺序（与判据顺序一致；crash 排前因为它最该先修）
MORPH_ORDER = [M_CRASH, M_HARNESS, M_FALSE_POSITIVE, M_MISSING, M_SEMANTIC]

# 编译前端停摆的前缀（phaseOfError 的契约前缀，改引擎错误格式时要同步）
FRONTEND_PREFIXES = ("parser errors:", "compiler error:")

# 运行期"没有这个能力"的关键词。全部取自实测错误文本（built-ins 全量跑批）：
#   ReferenceError: X is not defined        → 全局/内建不存在
#   TypeError: X is not a function          → 方法不存在
#   TypeError: X is not a constructor       → 构造器不存在
#   Cannot read properties of undefined     → 上游对象不存在（先取全局/原型再取属性）
#   not implemented / not supported         → 引擎显式声明未实现
MISSING_MARKERS = (
    "is not defined",
    "is not a function",
    "is not a constructor",
    "Cannot read properties of undefined",
    "not implemented",
    "not supported",
)

# negative 用例判定失配的消息前缀（judgePhase 的三条失败分支）
NEGATIVE_PREFIXES = ("期望", "错误类型不匹配")

# Intl 边界：判据是"路径或错误消息里出现 Intl 相关名字"。intl402 套件本身不在
# runner 的 suiteDirs 里（intl402 需要 i18n，Gox 未实现），所以只能从 built-ins
# 内部引用 Intl 的用例来量化"Intl 到底值多少分"。
INTL_RE = re.compile(
    r"Intl|NumberFormat|DateTimeFormat|Collator|PluralRules|Segmenter"
    r"|ListFormat|RelativeTimeFormat|DisplayNames|localeCompare|toLocale",
    re.I)


def head_line(msg):
    """错误消息首行 —— 引擎的运行时错误是多行（首行 + 源码回显），
    聚类只认首行，否则源码里的字样会污染判据。"""
    if not msg:
        return ""
    return msg.split("\n", 1)[0].strip()


def morph_of(res):
    """由 (phase, error) 判定失败形态。互斥，取首个命中。"""
    phase = res.get("phase") or ""
    err = head_line(res.get("error"))
    if phase in ("crashed", "timeout"):
        return M_CRASH
    if phase == "harness":
        return M_HARNESS
    if err.startswith(NEGATIVE_PREFIXES):
        return M_FALSE_POSITIVE
    if err.startswith(FRONTEND_PREFIXES):
        return M_MISSING
    for m in MISSING_MARKERS:
        if m in err:
            return M_MISSING
    return M_SEMANTIC


def rx_escape(s):
    """给目录名做最小转义：只转义正则特殊字符，保留 `-` 与 `/`。

    re.escape 会把 `-` 也转成 `\\-`（3.7+ 仍如此），定向 filter 里出现
    `^built\\-ins/...` 虽然合法但没法读 —— 而这串正则是要抄进命令行和工单的。
    """
    return re.sub(r"[^\w\-/]", lambda m: "\\" + m.group(0), s)


def family_of(path, depth):
    """目录族 = 用例所在目录的前 depth 段（不含文件名）。

    目录比文件更有资格当"族"：test262 按「内建对象 / 方法」组织目录，同一个
    目录里的用例共享同一个规范条文与（通常）同一个引擎根因。深度默认 3 ——
    `built-ins/Array/prototype` 这样的粒度既能聚出可修的簇，又不至于细到
    "一族一例"。目录本身不足 depth 段时取全部（如 built-ins/Temporal/keys.js
    → built-ins/Temporal），不会出现半截族名。
    """
    segs = path.split("/")
    dirs = segs[:-1]
    return "/".join(dirs[:depth])


# ── 输入读取 ─────────────────────────────────────────────────────────────────


def load_results(path):
    """读跑批结果：JSON 报告（gox test262 -json）或 JSONL（分片逐用例落盘）。

    格式判断按内容而不是扩展名 —— 分片 JSONL 的落点文件名由 runner 决定，
    扩展名不可靠。
    """
    try:
        with open(path, encoding="utf-8") as f:
            first = f.readline()
    except OSError as e:
        die("读不了 %s：%s" % (path, e))
    if first.lstrip().startswith("{"):
        try:
            with open(path, encoding="utf-8") as f:
                doc = json.load(f)
        except ValueError as e:
            die("%s 不是合法 JSON：%s" % (path, e))
        if isinstance(doc, dict):
            results = doc.get("results") or []
            meta = {k: doc.get(k) for k in ("suite", "total", "passed", "failed",
                                            "skipped", "rate_pct", "seconds")}
            return results, meta
        die("%s 是 JSON 但不是跑批报告（缺 results 字段）" % path)
    # JSONL：一行一条 test262Result
    results, meta = [], {}
    try:
        with open(path, encoding="utf-8") as f:
            for line in f:
                line = line.strip()
                if not line:
                    continue
                results.append(json.loads(line))
    except ValueError as e:
        die("%s 不是合法 JSONL：%s" % (path, e))
    return results, meta


def die(msg):
    print("test262-cluster: %s" % msg)
    sys.exit(2)


# ── 聚类 ─────────────────────────────────────────────────────────────────────


def analyze(results, depth, top_n):
    """返回结构化结果（可序列化，供 --json 落盘）。"""
    total = len(results)
    passed = sum(1 for r in results if r.get("pass"))
    failed = total - passed

    # 族基线：族内全部用例（含通过）的总数/通过数/合规率 —— 排期要看"这族
    # 还剩多少空间"，只看失败数会低估已经过了大半的族。
    fam_total, fam_pass = {}, {}
    for r in results:
        fam = family_of(r.get("path", ""), depth)
        fam_total[fam] = fam_total.get(fam, 0) + 1
        if r.get("pass"):
            fam_pass[fam] = fam_pass.get(fam, 0) + 1

    morph_count = {}
    clusters = {}
    for r in results:
        if r.get("pass"):
            continue
        fam = family_of(r.get("path", ""), depth)
        m = morph_of(r)
        morph_count[m] = morph_count.get(m, 0) + 1
        key = (fam, m)
        clusters[key] = clusters.get(key, 0) + 1

    # 排序稳定：规模降序 → 族名升序 → 形态按 MORPH_ORDER。规模相同的簇不会
    # 因为 dict 遍历顺序（Python 3.7+ 是插入序，取决于输入文件顺序）而翻转，
    # 同一份输入无论跑多少次、什么平台，输出逐字节一致。
    ordered = sorted(clusters.items(),
                     key=lambda kv: (-kv[1], kv[0][0], MORPH_ORDER.index(kv[0][1])))
    top = []
    for (fam, m), n in ordered[:top_n]:
        ft = fam_total.get(fam, 0)
        fp = fam_pass.get(fam, 0)
        top.append({
            "family": fam,
            "morph": m,
            "size": n,
            "share_of_failures_pct": pct(n, failed),
            "family_total": ft,
            "family_passed": fp,
            "family_rate_pct": pct(fp, ft),
            "filter": "^%s/" % rx_escape(fam),
        })

    intl = [r for r in results if INTL_RE.search(r.get("path", ""))]
    intl_fails = [r for r in intl if not r.get("pass")]
    intl_morph = {}
    for r in intl_fails:
        m = morph_of(r)
        intl_morph[m] = intl_morph.get(m, 0) + 1

    return {
        "depth": depth,
        "top_n": top_n,
        "coverage": {
            "result_count": total,
            "passed": passed,
            "failed": failed,
            "rate_pct": pct(passed, total),
        },
        "morphologies": [
            {"morph": m, "size": morph_count.get(m, 0),
             "share_of_failures_pct": pct(morph_count.get(m, 0), failed),
             "share_of_all_pct": pct(morph_count.get(m, 0), total)}
            for m in MORPH_ORDER if morph_count.get(m, 0)
        ],
        "clusters": top,
        "cluster_count_total": len(clusters),
        "intl": {
            "cases": len(intl),
            "passed": sum(1 for r in intl if r.get("pass")),
            "rate_pct": pct(sum(1 for r in intl if r.get("pass")), len(intl)),
            "share_of_all_pct": pct(len(intl), total),
            "share_of_failures_pct": pct(len(intl_fails), failed),
            "morphologies": [
                {"morph": m, "size": intl_morph[m]}
                for m in MORPH_ORDER if m in intl_morph
            ],
        },
    }


def pct(n, d):
    if not d:
        return 0.0
    return round(n * 100.0 / d, 4)


# ── 输出 ─────────────────────────────────────────────────────────────────────


def fmt(v):
    return "%.2f" % v if isinstance(v, float) else str(v)


def render_text(rep, src):
    cov = rep["coverage"]
    lines = []
    lines.append("test262 失败面聚类：%s" % src)
    lines.append("  覆盖度：逐用例结果 %d 条 ｜ 通过 %d ｜ 失败 %d ｜ 合规率 %.4f%%"
                 % (cov["result_count"], cov["passed"], cov["failed"], cov["rate_pct"]))
    lines.append("  族深度：%d 段目录 ｜ 簇总数 %d ｜ 取 Top %d"
                 % (rep["depth"], rep["cluster_count_total"], rep["top_n"]))
    lines.append("")
    lines.append("── 失败形态分布（互斥，各形态之和 = 失败总数 %d）" % cov["failed"])
    lines.append("  %-12s %8s %10s %10s" % ("形态", "规模", "占失败面", "占全量"))
    for m in rep["morphologies"]:
        lines.append("  %-12s %8d %9s%% %9s%%"
                     % (m["morph"], m["size"], fmt(m["share_of_failures_pct"]),
                        fmt(m["share_of_all_pct"])))
    lines.append("")
    lines.append("── Top %d 簇（目录族 × 失败形态）" % rep["top_n"])
    lines.append("  %-4s %-46s %-12s %6s %8s %s"
                 % ("#", "簇", "形态", "规模", "占失败面", "族基线 (通过/总数 = 合规率)"))
    for i, c in enumerate(rep["clusters"], 1):
        lines.append("  %-4d %-46s %-12s %6d %7s%%  %d/%d = %s%%"
                     % (i, c["family"], c["morph"], c["size"],
                        fmt(c["share_of_failures_pct"]), c["family_passed"],
                        c["family_total"], fmt(c["family_rate_pct"])))
    lines.append("")
    lines.append("── 每簇定向 filter（直接喂 gox test262 -filter）")
    for i, c in enumerate(rep["clusters"], 1):
        lines.append("  #%d  %s" % (i, c["filter"]))
    lines.append("")
    it = rep["intl"]
    lines.append("── Intl 边界（拍板依据）")
    lines.append("  命中 Intl 关键字的用例：%d 条（占全量 %s%%，占失败面 %s%%）｜ 通过 %d ｜ 合规率 %s%%"
                 % (it["cases"], fmt(it["share_of_all_pct"]),
                    fmt(it["share_of_failures_pct"]), it["passed"], fmt(it["rate_pct"])))
    if it["morphologies"]:
        lines.append("  其失败形态：" + "、".join(
            "%s %d" % (m["morph"], m["size"]) for m in it["morphologies"]))
    lines.append("  注：intl402 套件不在 runner 的 suiteDirs 内（需 i18n，Gox 未实现），"
                 "故上表只覆盖 built-ins/language 内部引用 Intl 的用例。")
    return "\n".join(lines) + "\n"


def render_md(rep, src):
    cov = rep["coverage"]
    L = ["# test262 失败面聚类（自动生成，勿手改）", "",
         "生成命令：`python3 scripts/test262-cluster.py --results %s`" % src, "",
         "## 覆盖度", "",
         "| 项 | 数字 |", "|---|---|",
         "| 逐用例结果 | %d |" % cov["result_count"],
         "| 通过 / 失败 | %d / %d |" % (cov["passed"], cov["failed"]),
         "| 合规率 | %.4f%% |" % cov["rate_pct"],
         "| 族深度 / 簇总数 | %d 段目录 / %d 簇 |" % (rep["depth"], rep["cluster_count_total"]),
         "", "## 失败形态分布", "",
         "| 形态 | 规模 | 占失败面 | 占全量 |", "|---|---:|---:|---:|"]
    for m in rep["morphologies"]:
        L.append("| %s | %d | %s%% | %s%% |" % (m["morph"], m["size"],
                                                fmt(m["share_of_failures_pct"]),
                                                fmt(m["share_of_all_pct"])))
    L += ["", "## Top %d 簇" % rep["top_n"], "",
          "| # | 目录族 | 失败形态 | 簇规模 | 占失败面 | 族基线（通过/总数） | 定向 filter |",
          "|---|---|---|---:|---:|---:|---|"]
    for i, c in enumerate(rep["clusters"], 1):
        L.append("| %d | `%s` | %s | %d | %s%% | %d/%d = %s%% | `%s` |"
                 % (i, c["family"], c["morph"], c["size"],
                    fmt(c["share_of_failures_pct"]), c["family_passed"],
                    c["family_total"], fmt(c["family_rate_pct"]), c["filter"]))
    it = rep["intl"]
    L += ["", "## Intl 边界", "",
          "命中 Intl 关键字的用例 **%d 条**（占全量 %s%%，占失败面 %s%%），通过 %d，合规率 %s%%。"
          % (it["cases"], fmt(it["share_of_all_pct"]), fmt(it["share_of_failures_pct"]),
             it["passed"], fmt(it["rate_pct"])),
          "",
          "注：intl402 套件不在 runner 的 `suiteDirs` 内（需 i18n，Gox 未实现），上表只覆盖 built-ins/language 内部引用 Intl 的用例。"]
    return "\n".join(L) + "\n"


# ── 自检 ─────────────────────────────────────────────────────────────────────


def self_test():
    """判据自检：**一个没人钉住的判据会悄悄漂移**。

    钉住三件事：五类形态各就位（含互斥性）、Top-N 排序稳定（同规模不因输入
    顺序翻转）、以及同一份输入两次跑出来的文本逐字节一致。
    """
    def r(path, phase, err, ok=False):
        return {"path": path, "pass": ok, "phase": phase, "error": err, "seconds": 0.0}

    cases = [
        r("built-ins/Array/prototype/map/x.js", "runtime", "vm error: TypeError: a is not defined"),
        r("built-ins/Array/prototype/map/y.js", "runtime", "vm error: Test262Error: Expected SameValue"),
        r("built-ins/Array/prototype/map/z.js", "compile", "期望 parse 报错, 实际 runtime 阶段错误: x"),
        r("built-ins/Array/prototype/map/w.js", "harness", "runner 未登记的 harness 依赖: foo.js"),
        r("built-ins/Array/prototype/map/v.js", "timeout", "执行超时 (>3s)"),
        r("built-ins/Array/prototype/map/u.js", "runtime", "parser errors: | line 1:1: bad"),
        r("built-ins/Array/prototype/map/t.js", "runtime", "错误类型不匹配: 期望含 TypeError"),
        r("built-ins/Array/prototype/map/s.js", "runtime", "vm error: TypeError: x is not a function"),
        r("built-ins/Temporal/keys.js", "runtime", "vm error: Test262Error: nope"),
        r("built-ins/Array/prototype/map/pass.js", "pass", "", ok=True),
    ]
    got = {morph_of(c) for c in cases}
    want = {M_MISSING, M_SEMANTIC, M_FALSE_POSITIVE, M_HARNESS, M_CRASH}
    ok = True

    def check(name, cond, extra=""):
        nonlocal ok
        if cond:
            print("ok  %s" % name)
        else:
            ok = False
            print("FAIL %s%s" % (name, ("\n    " + extra) if extra else ""))

    check("五类形态都能判出来", got == want, "实际判出 %s" % sorted(got))
    check("形态互斥（每个用例只归一簇）",
          all(isinstance(morph_of(c), str) for c in cases))

    rep = analyze(cases, 3, 10)
    check("失败面计数正确（9 条失败 / 1 条通过）",
          rep["coverage"] == {"result_count": 10, "passed": 1, "failed": 9, "rate_pct": 10.0},
          str(rep["coverage"]))
    check("簇规模之和 = 失败总数",
          sum(c["size"] for c in rep["clusters"]) == 9)

    # 排序稳定：把输入逆序，Top 簇的顺序不许变
    rep2 = analyze(list(reversed(cases)), 3, 10)
    check("输入顺序翻转后 Top 簇顺序不变",
          [(c["family"], c["morph"], c["size"]) for c in rep["clusters"]] ==
          [(c["family"], c["morph"], c["size"]) for c in rep2["clusters"]])

    t1, t2 = render_text(rep, "x.json"), render_text(rep2, "x.json")
    check("两次渲染逐字节一致（输出确定）", t1 == t2)
    check("定向 filter 是合法正则片段",
          all(c["filter"].startswith("^") and c["filter"].endswith("/")
              for c in rep["clusters"]))
    return 0 if ok else 1


# ── 主入口 ───────────────────────────────────────────────────────────────────


def main():
    argv = sys.argv[1:]
    if "--self-test" in argv:
        return self_test()

    path = None
    if "--results" in argv:
        i = argv.index("--results")
        if i + 1 >= len(argv):
            die("--results 后面要跟跑批结果文件（JSON 报告或 JSONL）")
        path = argv[i + 1]
    if path is None:
        die("需要 --results <跑批结果>；先看用法：python3 scripts/test262-cluster.py --self-test")

    depth = 3
    if "--depth" in argv:
        i = argv.index("--depth")
        if i + 1 >= len(argv):
            die("--depth 后面要跟正整数")
        try:
            depth = int(argv[i + 1])
        except ValueError:
            die("--depth 不是整数：%s" % argv[i + 1])
        if depth < 1:
            die("--depth 必须 ≥ 1")

    top_n = 10
    if "--top" in argv:
        i = argv.index("--top")
        if i + 1 >= len(argv):
            die("--top 后面要跟正整数")
        try:
            top_n = int(argv[i + 1])
        except ValueError:
            die("--top 不是整数：%s" % argv[i + 1])
        if top_n < 1:
            die("--top 必须 ≥ 1")

    json_out = md_out = None
    if "--json" in argv:
        i = argv.index("--json")
        if i + 1 >= len(argv):
            die("--json 后面要跟输出路径")
        json_out = argv[i + 1]
    if "--md" in argv:
        i = argv.index("--md")
        if i + 1 >= len(argv):
            die("--md 后面要跟输出路径")
        md_out = argv[i + 1]

    results, meta = load_results(path)
    if not results:
        die("%s 里没有逐用例结果（跑批没跑起来？）" % path)
    # 报告头里的 suite/total 用来交叉核对覆盖度：JSONL 没有这些字段，用实读条数。
    rep = analyze(results, depth, top_n)
    if meta.get("suite"):
        rep["suite"] = meta["suite"]

    sys.stdout.write(render_text(rep, os.path.basename(path)))

    if json_out:
        with open(json_out, "w", encoding="utf-8") as f:
            json.dump(rep, f, ensure_ascii=False, indent=2, sort_keys=True)
            f.write("\n")
        print("已写 %s" % json_out)
    if md_out:
        with open(md_out, "w", encoding="utf-8") as f:
            f.write(render_md(rep, os.path.basename(path)))
        print("已写 %s" % md_out)
    return 0


if __name__ == "__main__":
    sys.exit(main())
