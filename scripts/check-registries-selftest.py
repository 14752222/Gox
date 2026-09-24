#!/usr/bin/env python3
"""check-registries.py 的负向自测：往源码里注入故障，断言检查**真的会红**。

为什么需要这个文件：**一个从没失败过的守卫等于黑盒**。注册表检查本身也是代码，
它可能因为锚点改名、正则失配、豁免表写错而永远输出 "ok" —— 那时它不但没有防线，
还会让人误以为有防线。这里的每个用例都对应一类真实事故（删掉一个绘制分支、
新组件忘了登记、模块重复注册、版本号脱钩、包里没有二进制…），注入后必须
被对应检查抓住，且失败理由要落在预期的关键字上。

做法：把仓库镜像到临时目录（只带 .go / npm/ / scripts/，不带二进制与 dist/），
注入故障，跑镜像里的 check-registries.py，断言退出码非 0 且输出含预期关键字。

用法：
    python3 scripts/check-registries-selftest.py

本脚本**不进 CI 的常规路径** —— 它要把仓库镜像 11 次（本机 Windows + 杀软
实测约 2 分钟，而检查本身只要 1 秒；Linux 上会快得多，瓶颈是文件系统/AV 扫描）。
改了 check-registries.py 的抽取逻辑或豁免表之后，本地跑一遍。
"""
from __future__ import annotations

import os
import shutil
import subprocess
import sys
import tempfile

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SKIP = {".git", ".mimosa", ".workbuddy", ".idea", "dist", "node_modules"}


def mirror(dst):
    """镜像出检查所需的最小仓库（源码 + npm 包目录 + scripts/）。"""
    for dirpath, dirnames, filenames in os.walk(ROOT):
        dirnames[:] = [d for d in dirnames if d not in SKIP]
        for fn in filenames:
            src = os.path.join(dirpath, fn)
            rel = os.path.relpath(src, ROOT).replace("\\", "/")
            # npm/ 与 scripts/ 整目录都要。注意 os.walk 的 dirpath 到 npm/bin
            # 那一层就不再以 "npm" 结尾，所以只能按 rel 判前缀。
            if not (rel.endswith(".go") or rel.startswith("npm/")
                    or rel.startswith("scripts/")):
                continue
            if fn.endswith(".exe"):
                continue
            out = os.path.join(dst, rel)
            os.makedirs(os.path.dirname(out), exist_ok=True)
            shutil.copy2(src, out)


def sub(root, rel, old, new):
    p = os.path.join(root, rel)
    with open(p, encoding="utf-8") as f:
        s = f.read()
    if old not in s:
        raise SystemExit("注入锚点没找到（源码改了就要同步改自测）：%s :: %r" % (rel, old[:70]))
    with open(p, "w", encoding="utf-8") as f:
        f.write(s.replace(old, new, 1))


def run(root):
    r = subprocess.run([sys.executable,
                        os.path.join(root, "scripts", "check-registries.py")],
                       capture_output=True, text=True)
    return r.returncode, r.stdout + r.stderr


# (用例名, 期望在失败信息里出现的关键字, 注入动作)
CASES = [
    ("删掉 raster.go 的绘制分支",
     "没有分支",
     lambda r: sub(r, "gfx/raster.go", '\tcase "slider":\n', "")),

    ("新组件没登记就加 case",
     "不在 gfx/node.go 的 knownTags 里",
     lambda r: sub(r, "gfx/raster.go", '\tcase "slider":',
                   '\tcase "widget":\n\t\tpaintBoxDecor(img, n, disabled)\n\tcase "slider":')),

    ("豁免项后来被真的实现了",
     "但代码里存在该分支",
     lambda r: sub(r, "gfx/layout.go", '\tcase "menuitem":',
                   '\tcase "dialog":\n\t\tif w == 0 {\n\t\t\tw = 400\n\t\t}\n\tcase "menuitem":')),

    ("knownTags 里错拼一个标签名",
     "没有分支",
     lambda r: sub(r, "gfx/node.go", '"slider": {}', '"slidr": {}')),

    ("同一模块重复注册",
     "被注册了",
     lambda r: sub(r, "gfx/view.go", 'object.RegisterBuiltinModule("gx/view"',
                   'object.RegisterBuiltinModule("gx/dev", func() map[string]object.Value {\n'
                   '\t\treturn map[string]object.Value{"dup": object.NewBuiltin("dup", jsViewSwitch)}\n'
                   '\t})\n\tobject.RegisterBuiltinModule("gx/view"')),

    ("新模块忘了进 submodules",
     "没进 stdlib/gox_module.go 的 submodules",
     lambda r: sub(r, "stdlib/gox_module.go", '"gx/screen",', "")),

    ("submodules 列了不存在的模块",
     "仓库里没有任何",
     lambda r: sub(r, "stdlib/gox_module.go", '"gx/screen",',
                   '"gx/nonexistent",\n\t\t\t"gx/screen",')),

    ("两个子模块导出同名 API",
     "同时被",
     lambda r: sub(r, "gfx/view.go",
                   '"Switch": object.NewBuiltin("Switch", jsViewSwitch),',
                   '"Switch": object.NewBuiltin("Switch", jsViewSwitch),\n'
                   '\t\t\t"alert": object.NewBuiltin("alert", jsViewSwitch),')),

    ("main.go 与 package.json 版本号脱钩",
     "版本号不一致",
     lambda r: sub(r, "main.go", 'const version = "0.5.0"', 'const version = "0.6.0"')),

    ("package.json files 漏掉 binaries/",
     "没有 binaries/",
     lambda r: sub(r, "npm/package.json", '"binaries/"', '"binaries-typo/"')),

    ("package.json bin 指向不存在的文件",
     "但该文件不存在",
     lambda r: sub(r, "npm/package.json", '"gox": "bin/gox.js"', '"gox": "bin/gone.js"')),
]


def main():
    ok = True
    # 只遍历仓库一次：先做一份"干净镜像"，每个用例再 copytree 一份出来。
    # （直接在循环里 walk 仓库的话，11 个用例要遍历 11 遍，本机实测 117s → 20s。）
    pristine = tempfile.mkdtemp(prefix="gox-reg-pristine-")
    try:
        mirror(pristine)
        for name, expect, inject in CASES:
            tmp = tempfile.mkdtemp(prefix="gox-reg-case-")
            shutil.copytree(pristine, tmp, dirs_exist_ok=True)
            try:
                code, out = run(tmp)
                if code != 0:
                    print("!! 基线就红了（镜像不完整?）：%s\n%s" % (name, out))
                    ok = False
                    continue
                inject(tmp)
                code, out = run(tmp)
                if code == 0:
                    print("!! 没抓到：%s" % name)
                    ok = False
                elif expect not in out:
                    print("!! 抓到了但理由不对：%s\n   期望含 %r\n   实际输出：\n%s"
                          % (name, expect, out))
                    ok = False
                else:
                    hit = next((l for l in out.splitlines() if expect in l), "")
                    print("ok  %-32s -> %s" % (name, hit.strip()[:92]))
            finally:
                shutil.rmtree(tmp, ignore_errors=True)
    finally:
        shutil.rmtree(pristine, ignore_errors=True)
    print("\n%s" % ("全部 %d 个负向用例通过。" % len(CASES) if ok
                    else "存在失败用例 —— 守卫有洞，别急着信它。"))
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
