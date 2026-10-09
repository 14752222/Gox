#!/usr/bin/env python3
"""test262 合规率下限闸门 —— 与 .github/workflows/test262.yml 配套（rKpKpD 第 3 条）。

背景：test262.yml 每次跑完把合规率写成 shields.io 徽章提交回
`docs/test262-compliance.json`，但**不设任何 fail 条件** —— 合规率下滑时 CI 是
绿的，只有人去读徽章上的数字才会发现。这一单给它一条下限。

## 判据：为什么不是简单比「率」

合规率 = passed / total，而 total 会随 test262 上游更新而变化（浅克隆每次都拉
最新）。上游一次加几百个用例，率就会自然下滑几个百分点 —— 那不是回归，判红
就是噪声。所以分两种情况：

- **用例集没变**（total 变化 ≤ 1%）：直接比率，跌过容差即红。这是最常见的
  情况，也是本闸门真正要看的：改了引擎 ⇒ 原本过的用例现在不过了。
- **用例集变了**（total 变化 > 1%）：率不可比，降级为**提示**；改用 passed
  的绝对数看 —— 分母变了，"过的用例少了"依然是硬信号。同时把用例集变化量
  打出来，让人一眼看出是不是上游更新的影响。

容差取 0.5 个百分点：合规率是对外披露的数字，涨一点跌一点都常见，但跌掉半个
百分点通常意味着**一整簇用例**回归（language 全量约 2.3 万例，0.5pp ≈ 115 例），
值得当次就查，而不是等有人看徽章。

## 用法

    python3 scripts/check-compliance.py --current test262-results.json \
                                        --baseline docs/test262-baseline.json
    python3 scripts/check-compliance.py --current <f> --update-baseline
    python3 scripts/check-compliance.py --github       # 失败时额外输出 ::error::
    python3 scripts/check-compliance.py --self-test    # 闸门自检

退出码：0 = 通过（含"只提示"）；1 = 合规率回归；2 = 用法/数据错误。
"""
from __future__ import annotations

import datetime
import json
import os
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DEFAULT_BASELINE = os.path.join(ROOT, "docs", "test262-baseline.json")

# 合规率容差（百分点）。见文件头：0.5pp ≈ language 全量的 115 例。
TOL_RATE_PP = 0.5
# 用例集变化的判定门槛：total 相对变化超过它，就认为「率不可比」，切换到 passed 判据。
TOL_TOTAL_PCT = 1.0
# 用例集变化时，passed 绝对数允许的下降幅度（%）。
TOL_PASSED_PCT = 1.0


def die(msg):
    print("compliance-gate: %s" % msg)
    sys.exit(2)


def load(path):
    try:
        with open(path, encoding="utf-8") as f:
            return json.load(f)
    except (OSError, ValueError) as e:
        die("读不了 %s：%s" % (path, e))


def pp(delta):
    """百分点差值，避免浮点尾巴（78.400000000001 - 78.4）。"""
    return round(delta, 4)


def fmt(v):
    """表格里的数字：率统一 4 位小数，否则 `78.36550619573464` 会把列撑歪。"""
    if isinstance(v, float):
        return "%.4f" % v
    return str(v)


def judge(cur, base):
    """返回 ( regressions, notes, rows )。"""
    c_rate = float(cur["rate_pct"])
    c_pass = int(cur["passed"])
    c_total = int(cur["total"])
    b_rate = float(base["rate_pct"])
    b_pass = int(base["passed"])
    b_total = int(base["total"])

    regressions, notes, rows = [], [], []
    rows.append(("合规率 (%)", b_rate, c_rate, pp(c_rate - b_rate)))
    rows.append(("通过数", b_pass, c_pass, c_pass - b_pass))
    rows.append(("用例总数", b_total, c_total, c_total - b_total))

    if c_total <= 0 or b_total <= 0:
        notes.append("总数为 0（跑批没跑起来？），跳过判定")
        return regressions, notes, rows

    total_delta_pct = abs(c_total - b_total) / float(b_total) * 100.0

    if total_delta_pct > TOL_TOTAL_PCT:
        # 用例集变了：率不可比，只提示；改看 passed 绝对数
        notes.append(
            "用例集变了：total %d → %d（%+.1f%% > %.1f%%）—— 通常是 test262 上游更新，"
            "此时「率」不可比，本轮改用通过数的绝对变化判定"
            % (b_total, c_total, (c_total - b_total) / float(b_total) * 100.0,
               TOL_TOTAL_PCT))
        if c_rate < b_rate:
            notes.append("合规率 %.4f%% → %.4f%%（%+.2fpp）—— 用例集变动下仅供参考"
                         % (b_rate, c_rate, pp(c_rate - b_rate)))
        if b_pass > 0 and c_pass < b_pass:
            drop_pct = (b_pass - c_pass) / float(b_pass) * 100.0
            if drop_pct > TOL_PASSED_PCT:
                regressions.append(
                    "通过数从 %d 掉到 %d（-%d，-%.2f%% > %.2f%%）—— 用例集变动下"
                    "「过的用例变少」依然是硬信号：要么是回归，要么是被删/被跳的用例"
                    % (b_pass, c_pass, b_pass - c_pass, drop_pct, TOL_PASSED_PCT))
        return regressions, notes, rows

    drop = pp(b_rate - c_rate)
    if drop <= 0:
        if drop < 0:
            notes.append("合规率上升 %+.2fpp（%.4f%% → %.4f%%）—— 好事，不判红"
                         % (-drop, b_rate, c_rate))
        return regressions, notes, rows

    if drop <= TOL_RATE_PP:
        notes.append("合规率下降 %.2fpp（%.4f%% → %.4f%%），在容差 %.1fpp 之内"
                     % (drop, b_rate, c_rate, TOL_RATE_PP))
        return regressions, notes, rows

    regressions.append(
        "合规率从 %.4f%% 跌到 %.4f%%（−%.2fpp，容差 %.1fpp）—— 按 language 全量折算 "
        "约 %d 个用例从「过」变成「不过」，同一套用例集下这是回归，不是噪声"
        % (b_rate, c_rate, drop, TOL_RATE_PP,
           int(round(drop / 100.0 * c_total))))
    return regressions, notes, rows


def main():
    argv = sys.argv[1:]
    if "--self-test" in argv:
        return self_test()

    cur_path = None
    if "--current" in argv:
        i = argv.index("--current")
        if i + 1 >= len(argv):
            die("--current 后面要跟 test262-results.json 路径")
        cur_path = argv[i + 1]
    base_path = DEFAULT_BASELINE
    if "--baseline" in argv:
        i = argv.index("--baseline")
        if i + 1 >= len(argv):
            die("--baseline 后面要跟基线文件路径")
        base_path = argv[i + 1]
    github = "--github" in argv
    update = "--update-baseline" in argv
    if cur_path is None:
        die("需要 --current <test262-results.json>")

    cur = load(cur_path)
    for k in ("rate_pct", "passed", "total"):
        if k not in cur:
            die("%s 里缺字段 %s —— 跑批输出的格式变了？" % (cur_path, k))

    if update:
        base = {
            "说明": "test262 合规率基线 —— 由 test262.yml 在每次跑批通过后自动更新"
                    "（scripts/check-compliance.py --update-baseline）。它是「上一次"
                    "通过闸门时的合规率」，不是人工台账。",
            "生成方式": "python3 scripts/check-compliance.py --current <results> --update-baseline",
            "generated_at": (os.environ.get("GITHUB_RUN_STARTED_AT")
                             or datetime.datetime.now(datetime.timezone.utc)
                             .strftime("%Y-%m-%dT%H:%M:%SZ")),
            "commit": os.environ.get("GITHUB_SHA", ""),
            "suite": "language",
            "rate_pct": float(cur["rate_pct"]),
            "passed": int(cur["passed"]),
            "total": int(cur["total"]),
            "skipped": int(cur.get("skipped", 0)),
        }
        os.makedirs(os.path.dirname(base_path) or ".", exist_ok=True)
        with open(base_path, "w", encoding="utf-8") as f:
            json.dump(base, f, ensure_ascii=False, indent=2)
            f.write("\n")
        print("已写入基线 %s：%.4f%%（%d/%d）"
              % (os.path.relpath(base_path, ROOT), base["rate_pct"],
                 base["passed"], base["total"]))
        return 0

    if not os.path.isfile(base_path):
        print("!! 没有基线 %s —— 首次跑批请先建立基线："
              "python3 scripts/check-compliance.py --current <results> --update-baseline"
              % os.path.relpath(base_path, ROOT))
        return 0  # 首次不算失败：没有"上一次"可比

    base = load(base_path)
    regressions, notes, rows = judge(cur, base)

    print("test262 合规率闸门：%s" % os.path.relpath(cur_path, ROOT))
    print("  基线：%.4f%%（%d/%d）@ %s"
          % (base.get("rate_pct", 0), base.get("passed", 0), base.get("total", 0),
             str(base.get("commit", ""))[:8] or "?"))
    print("  %-16s %14s %14s %10s" % ("指标", "基线", "现值", "变化"))
    for name, b, c, d in rows:
        print("  %-16s %14s %14s %+10s" % (name, fmt(b), fmt(c), fmt(d)))
    for n in notes:
        print("  · %s" % n)
    print()

    if not regressions:
        print("ok  合规率无回归（容差 %.1fpp）" % TOL_RATE_PP)
        return 0

    print("FAIL 合规率回归：")
    for r in regressions:
        print("  - %s" % r)
    if github:
        print("::error::test262 合规率回归 —— %s"
              % " ｜ ".join(regressions)[:2000])
    return 1


def self_test():
    """闸门自检：**一个从没失败过的闸门等于黑盒**。

    四个合成用例分别钉住：真回归必须红、容差内的波动必须绿、用例集变动时
    必须切换到「通过数」判据、以及合规率上升不许红。
    """
    def res(rate, passed, total):
        return {"rate_pct": rate, "passed": passed, "total": total, "skipped": 0}

    base = res(78.4000, 18000, 22964)
    cases = [
        ("掉 1.2pp 必须红", res(77.2000, 17724, 22964), True, "合规率从"),
        ("掉 0.2pp 必须绿（容差内）", res(78.2000, 17954, 22964), False, ""),
        ("上升必须绿", res(79.0000, 18142, 22964), False, ""),
        # 用例集变大 5%：率跌 1pp 但通过数没少 ⇒ 不该红
        ("用例集变动 + 通过数不减 ⇒ 绿", res(77.4000, 18100, 24112), False, ""),
        # 用例集变动 + 通过数真的少了 3% ⇒ 红
        ("用例集变动 + 通过数少 3% ⇒ 红", res(75.0000, 17460, 24112), True, "通过数从"),
    ]
    ok = True
    for name, cur, want_red, kw in cases:
        regressions, _, _ = judge(cur, base)
        got_red = bool(regressions)
        if got_red != want_red:
            ok = False
            print("FAIL %s：期望%s，实际%s\n    %s"
                  % (name, "红" if want_red else "绿", "红" if got_red else "绿",
                     "\n    ".join(regressions)))
            continue
        if want_red and kw and kw not in " ".join(regressions):
            ok = False
            print("FAIL %s：红是红了，但输出里缺少 %r\n    %s"
                  % (name, kw, "\n    ".join(regressions)))
            continue
        print("ok  %s" % name)
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
