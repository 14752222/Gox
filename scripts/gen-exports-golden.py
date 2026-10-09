#!/usr/bin/env python3
"""内置模块导出名单的 golden 文件 —— 生成与比对（r3FFt6）。

背景：Gox 的**导出名单**（gx/* 各子模块导出什么、gox 伞入口能 import 什么）
散落在 Go 源码的 `object.RegisterBuiltinModule("gx/xxx", …)` 里。README、官网、
npm 包页、脚手架模板、docs/ 各手册都要跟着这份名单走，而它们全是手写的 ——
漏改一处不会报错，只会让读者抄到一个不存在的名字（自 2026-09-22 起
「从内置模块 import 不存在的名字」已经是**编译期报错**，示例一抄就挂）。

已有的 scripts/check-imports.py 是「文档 → 源码」方向的核对，但它每次现抽源码、
不留痕：没法回答「上一版到这一版，导出面变了多少」。golden 文件补的就是这个
**可演进**的维度：名单落成一份机器可读的、进 git 的文件，于是

  * 导出面的每一次变化都会在 `git diff` 里现形（新增/删除/改名），
    评审时看得见，而不是混在一次大提交里；
  * 下游（README / 官网 / npm 包页 / 脚手架）有了一个可引用的唯一真相；
  * CI 用「实抽 vs golden」diff 兜住「改了代码忘了更新 golden」。

为什么**不**自动生成 golden（而要人跑一次脚本并提交）：导出面的变化属于 API
变更，理应被人看见并写进变更说明；CI 里自动刷新等于把这个动作藏起来 ——
那 golden 就退化成一份永远与代码一致的冗余文件，失去它唯一的价值。

用法：
    python3 scripts/gen-exports-golden.py            # 重新生成（导出面变更后跑）
    python3 scripts/gen-exports-golden.py --check    # 只比对，不写（CI 用）
    python3 scripts/gen-exports-golden.py --print    # 打到 stdout，不写文件

退出码：--check 下不一致为 1，一致为 0。
"""
from __future__ import annotations

import importlib.util
import json
import os
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
GOLDEN_REL = os.path.join("docs", "exports.golden.json")


def load_check_registries():
    """按文件加载 scripts/check-registries.py，复用它的抽取逻辑。

    抽取逻辑**只有一份**（写在 check-registries.py 里）：golden 与 CI 检查若各写
    一份正则，两边迟早漂移 —— 那时要查的就不是"导出面变了"，而是"哪个脚本的
    正则错了"，正是本单要消灭的那类噪音。
    """
    path = os.path.join(ROOT, "scripts", "check-registries.py")
    spec = importlib.util.spec_from_file_location("check_registries", path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)  # 该脚本的 main() 在 __name__ == "__main__" 下才跑
    return mod


def current(cr):
    """从源码实抽导出名单，返回给 golden 用的规范化结构。"""
    exports = cr.extract_module_exports()
    submodules = cr.extract_umbrella_submodules()

    modules = {}
    for name in sorted(exports):
        # extract_module_exports 返回的可能是 set，排序后写文件才能稳定 diff
        modules[name] = sorted(exports[name])

    # gox 伞入口：submodules 的并集（与 stdlib/gox_module.go 的聚合口径一致）
    umbrella = set()
    unknown = []
    for name in submodules:
        if name in modules:
            umbrella |= set(modules[name])
        else:
            unknown.append(name)
    if unknown:
        # 伞清单里写了一个没有注册的模块 —— 聚合入口会少一半导出而不报错，
        # 这是 check-registries 的既有检查项，这里只是不把它算进并集
        print("警告：submodules 里以下模块没有注册记录，未计入 gox 并集：%s"
              % " ".join(sorted(unknown)), file=sys.stderr)

    return {
        "说明": "本文件由 python3 scripts/gen-exports-golden.py 生成，"
                "是内置模块导出名单的唯一机器可读真相。改了 Go 侧的导出后，"
                "请重新生成并提交 —— CI 会用「实抽 vs 本文件」diff 兜住漏改。",
        "生成方式": "python3 scripts/gen-exports-golden.py",
        "真源": "Go 侧的 object.RegisterBuiltinModule(\"gx/xxx\", …) 与 "
                "stdlib/gox_module.go 的 submodules 清单",
        "moduleCount": len(modules),
        "modules": modules,
        # 伞入口的并集：读者从 `import ... from \"gox\"` 能拿到的全部名字
        "gox": sorted(umbrella),
    }


def diff(golden, actual):
    """返回不一致说明列表（空 = 一致）。按模块分组，逐名列出差异。"""
    problems = []
    g_mods = golden.get("modules", {})
    a_mods = actual.get("modules", {})

    for name in sorted(set(g_mods) | set(a_mods)):
        g = set(g_mods.get(name, []))
        a = set(a_mods.get(name, []))
        if g == a:
            continue
        added = sorted(a - g)
        removed = sorted(g - a)
        bits = []
        if added:
            bits.append("代码里有、golden 里没有（新导出？）：%s" % " ".join(added))
        if removed:
            bits.append("golden 里有、代码里没有（改名或删了？）：%s" % " ".join(removed))
        problems.append("  - 模块 %s：%s" % (name, "；".join(bits)))

    if sorted(golden.get("gox", [])) != sorted(actual.get("gox", [])):
        g = set(golden.get("gox", []))
        a = set(actual.get("gox", []))
        problems.append("  - gox 伞入口并集：新增 %s；消失 %s"
                        % (" ".join(sorted(a - g)) or "（无）",
                           " ".join(sorted(g - a)) or "（无）"))
    return problems


def main():
    argv = sys.argv[1:]
    cr = load_check_registries()
    actual = current(cr)
    text = json.dumps(actual, ensure_ascii=False, indent=2, sort_keys=False) + "\n"

    if "--print" in argv:
        sys.stdout.write(text)
        return 0

    path = os.path.join(ROOT, GOLDEN_REL)

    if "--check" in argv:
        if not os.path.isfile(path):
            print("FAIL  导出名单 golden：%s 不存在 —— 先跑 "
                  "python3 scripts/gen-exports-golden.py 生成它" % GOLDEN_REL)
            return 1
        with open(path, encoding="utf-8") as f:
            try:
                golden = json.load(f)
            except ValueError as e:
                print("FAIL  导出名单 golden：%s 不是合法 JSON：%s" % (GOLDEN_REL, e))
                return 1
        problems = diff(golden, actual)
        if problems:
            print("FAIL  导出名单 golden 与代码不一致 —— "
                  "导出面变了要让人看见：确认无误后跑 "
                  "python3 scripts/gen-exports-golden.py 重新生成并随代码一起提交")
            print("\n".join(problems))
            return 1
        print("ok    导出名单 golden 与代码一致（%d 个模块）" % actual["moduleCount"])
        return 0

    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        f.write(text)
    print("已写入 %s（%d 个模块，gox 伞入口 %d 个名字）"
          % (GOLDEN_REL, actual["moduleCount"], len(actual["gox"])))
    return 0


if __name__ == "__main__":
    sys.exit(main())
