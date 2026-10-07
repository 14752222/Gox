#!/usr/bin/env python3
"""壳工程 ↔ 引擎版本「成对发布」闸门（决策三第 4 层）。

背景：移动端壳工程（Kotlin / Swift / ArkTS）与引擎（libgox.so / libgox.a）之间存在
JNI / NAPI 契约。**版本错配编译期拦不住，只在运行时崩**（症状是 UnsatisfiedLinkError、
符号找不到、或方法签名对不上）。所以壳工程必须显式声明它面向哪一代引擎，发布前
拿到闸门上比对。

约定（壳工程侧声明文件）：

    app/<platform>/gox-engine.json
    {"engineVersion": "0.9.0"}          # 精确绑定
    {"engineVersionRange": "0.9.x"}     # 绑定到某一代（只比 major.minor）

引擎版本真源：`cmd/gox/main.go` 的 `const version`（与 `check-registries.py` 检查3 同一处）。

判据与退出码：
  * 声明存在且相符           → 通过（打印 [ok]）
  * 声明存在但**不符**       → 退出码 1（硬闸门：这是发布契约被破坏，必须挡住）
  * 声明**不存在**           → 打印带证据的 `::error::`，**退出码 0**（优雅跳过）
    理由：本机制尚未在三个壳工程里落地（见 docs/mobile-distribution-decision.md §3.2）。
    在它落地之前把 CI 判红只会让人把闸门关掉；所以先让它「响而不红」，用 `--require`
    在机制落地后（或在某个壳工程已声明时）升级为硬性要求。

用法：
    python3 scripts/check-shell-engine-version.py                    # 读 cmd/gox/main.go 的版本
    python3 scripts/check-shell-engine-version.py --engine-version 0.9.0
    python3 scripts/check-shell-engine-version.py --require          # 缺声明也算失败
    python3 scripts/check-shell-engine-version.py --root <仓库根>
"""
from __future__ import annotations

import argparse
import json
import os
import re
import sys

PLATFORMS = ("android", "harmony", "ios")
DECL_NAME = "gox-engine.json"


def read_engine_version(root: str) -> str:
    """从 cmd/gox/main.go 的 `const version = "x.y.z"` 取引擎版本（唯一真源）。"""
    main_go = os.path.join(root, "cmd", "gox", "main.go")
    try:
        with open(main_go, encoding="utf-8") as f:
            src = f.read()
    except OSError as e:
        raise SystemExit("::error::读不到引擎版本真源 %s：%s" % (main_go, e))
    m = re.search(r'^const version = "([^"]+)"', src, re.M)
    if not m:
        raise SystemExit(
            "::error::%s 里找不到 `const version = \"...\"`（grep 原始输出：%s）"
            % (main_go, [l for l in src.splitlines() if "const version" in l] or "<无>")
        )
    return m.group(1)


def check_one(root: str, platform: str, ver: str) -> str:
    """返回 'ok' / 'missing' / 'mismatch'。mismatch 是硬失败。"""
    decl = os.path.join(root, "app", platform, DECL_NAME)
    if not os.path.isfile(decl):
        try:
            entries = sorted(os.listdir(os.path.join(root, "app", platform)))
        except OSError:
            entries = ["<app/%s 不存在>" % platform]
        print(
            "::error::壳工程 %s 没有版本声明文件 app/%s/%s —— 壳工程↔引擎版本错配是"
            "运行时才崩，本平台当前**无闸门**；证据：app/%s/ 下 = %s"
            % (platform, platform, DECL_NAME, platform, entries)
        )
        return "missing"

    rel = os.path.relpath(decl, root).replace(os.sep, "/")
    try:
        with open(decl, encoding="utf-8") as f:
            d = json.load(f)
    except (OSError, ValueError) as e:
        print("::error::%s 不是合法 JSON：%s" % (rel, e))
        return "mismatch"

    exact = d.get("engineVersion")
    rng = d.get("engineVersionRange")
    if exact is not None:
        ok = exact == ver
        why = "engineVersion=%s，引擎=%s" % (exact, ver)
    elif rng is not None:
        # 只支持 major.minor 形式（"0.9.x" / "0.9"）—— 够表达「壳工程只对某一代引擎负责」
        ok = ver.split(".")[:2] == str(rng).split(".")[:2]
        why = "engineVersionRange=%s，引擎=%s" % (rng, ver)
    else:
        ok, why = False, "声明里既没有 engineVersion 也没有 engineVersionRange"

    if ok:
        print("  [ok] %s：%s" % (rel, why))
        return "ok"
    print("::error::%s 与引擎版本不符：%s" % (rel, why))
    return "mismatch"


def main() -> int:
    ap = argparse.ArgumentParser(description="壳工程 ↔ 引擎版本成对发布闸门")
    ap.add_argument("--root", default=os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
    ap.add_argument("--engine-version", default=None)
    ap.add_argument("--require", action="store_true", help="缺声明也算失败（机制落地后用）")
    args = ap.parse_args()

    root = os.path.abspath(args.root)
    ver = args.engine_version or read_engine_version(root)
    print("引擎版本（真源 cmd/gox/main.go）：%s" % ver)

    results = {p: check_one(root, p, ver) for p in PLATFORMS}

    if any(r == "mismatch" for r in results.values()):
        print("::error::壳工程版本引用校验失败 —— 版本错配会在运行时才崩，发布前必须对齐")
        return 1

    missing = [p for p, r in results.items() if r == "missing"]
    if missing:
        msg = "壳工程 %s 尚无 %s，第 4 层闸门对其未生效（待办，不是失败）" % (
            "/".join(missing), DECL_NAME,
        )
        if args.require:
            print("::error::%s —— 已用 --require 升级为失败" % msg)
            return 1
        print("::error::%s" % msg)
        return 0

    print("::notice::壳工程版本引用校验通过（%d 个平台）" % len(PLATFORMS))
    return 0


if __name__ == "__main__":
    sys.exit(main())
