#!/usr/bin/env python3
"""文档 / 示例 import 与内置模块导出表一致性检查 —— 本地与 CI 共用同一份判据。

背景：2026-10-02 发现 docs/gui-guide.md 的折叠屏示例把 gx/screen 的 posture /
hinge / regions 写成从 gx/viewport 导入 —— 自 2026-09-22 起「从内置模块 import
不存在的名字」是**编译期报错**，这种示例已经让读者一抄就挂；而 testdata / 脚手架
里的文件模块相对导入则是**静默 undefined**（名字不存在不报错，只有跑测试才暴露）。
两类问题文档侧没有任何闸门，本脚本补上这一道。

两类检查：

1. **内置模块 import**（`from "gox"` / `from "gx/xxx"`）。
   真源 = Go 侧 `object.RegisterBuiltinModule("gx/xxx", …)` 注册表里的
   `"name":` 键（`gox` 聚合入口取 gox_module.go `submodules` 清单的并集）。
   文档/示例里每个导入名都必须在真源里 —— 否则编译期报错，读者一抄就挂。

2. **文件模块相对导入**（`from "./x.js"` / `from "../store.ts"`）。
   目标文件必须存在；目标文件必须真的 `export` 了被导入的名字 ——
   这一类不报错、静默 undefined，是"文档说的和实现不一致"的隐匿形态。

扫描范围：docs/、website/、testdata/、scaffold/template/ 下的文本源
（.md/.js/.jsx/.mjs/.ts/.tsx）以及仓库根 README.md。二进制与产物目录跳过。

豁免表 ALLOW_*：文档里**故意写错**的示例（教程的"常见错误"表会展示错误写法），
每一条都必须给真实理由，会原样出现在 CI 的 ::error:: 输出里。

用法：
    python3 scripts/check-imports.py            # 跑全部检查
    python3 scripts/check-imports.py --list     # 只打印抽出的导出表

退出码非 0 表示失败。
"""
from __future__ import annotations

import glob
import io
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# Go 侧注册方（新增内置模块时把文件加进这里 —— 漏加会让本脚本用旧表判案）。
GO_FILES = [
    "stdlib/gox_module.go", "stdlib/solid.go", "stdlib/storage.go", "stdlib/update_module.go",
    "gfx/dev.go", "gfx/native_app.go", "gfx/native_device.go", "gfx/native_geo.go",
    "gfx/native_media.go", "gfx/native_permission.go", "gfx/render.go", "gfx/router.go",
    "gfx/screen.go", "gfx/theme.go", "gfx/view.go", "gfx/viewport.go",
    # T10 无障碍: 注册 gx/a11y（focusOrder/focusNode/focusNext/focusPrev/roles）。
    # 漏加会让 docs/accessibility.md 的 import 示例被误判成「模块不存在」。
    "gfx/a11y.go",
]

TEXT_EXT = {".md", ".js", ".jsx", ".mjs", ".ts", ".tsx"}
SCAN_DIRS = ["docs", os.path.join("website"), "testdata", os.path.join("scaffold", "template")]
SCAN_FILES = ["README.md", os.path.join("app", "NATIVE-HOST.md")]

SKIP_DIRS = {".git", ".workbuddy", "node_modules", ".vitepress", "dist", ".mimosa", "vendor"}

# ── 豁免表：文档里故意写错的 import 示例 ─────────────────────────────────────
# (相对路径, 模块名, 导入名) -> 理由。必须写真实机制，不能写"以后改"。
ALLOW_BUILTIN = {
    ("docs/tutorial.md", "gx/view", "each"):
        "§3.5 常见错误表的**故意错误示例**：演示 import { each } from gx/view 会编译期报错"
        "（each/show 是元素级指令，任何模块都不导出）",
}

# 泛指占位符（不是某个具体模块）：模块名 -> 理由。
ALLOW_UNKNOWN_MODULE = {
    "gx/xxx": "docs/tutorial.md §1.1 的泛指占位符 —— 示意 import 语句的写法，不指具体模块",
}

import_stmt_re = re.compile(
    r'import\s+(?:\{([^}]*)\}|(\*\s+as\s+[A-Za-z_$][\w$]*)|([A-Za-z_$][\w$]*)|)\s*'
    r'from\s*[\'"]([^\'"]+)[\'"]', re.S)
ident_re = re.compile(r'^[A-Za-z_$][\w$]*$')


def parse_braced_names(braces: str):
    """'{ a, b as c, type T }' -> 导入的源名集合；非标识符片段（… 占位）跳过。"""
    out = set()
    for raw in braces.split(","):
        raw = raw.strip()
        if not raw:
            continue
        if raw.startswith("type "):
            raw = raw[5:].strip()
        name = raw.split(" as ")[0].strip()
        if name and ident_re.match(name):
            out.add(name)
    return out


# ---------- 1. 从 Go 注册表提取真实导出 ----------
def collect_exports():
    exports = {}  # module -> set(names)
    key_re = re.compile(r'^\s*"([A-Za-z_][A-Za-z0-9_]*)"\s*:', re.M)
    for rel in GO_FILES:
        path = os.path.join(ROOT, rel)
        if not os.path.isfile(path):
            print("!! 注册方文件不存在: %s（请更新本脚本的 GO_FILES）" % rel)
            sys.exit(1)
        src = io.open(path, encoding="utf-8").read()
        for m in re.finditer(r'RegisterBuiltinModule\("([A-Za-z0-9_/-]+)"', src):
            mod = m.group(1)
            tail = src[m.end():]
            end = tail.find("\n\t})")
            body = tail[:end] if end >= 0 else tail
            exports.setdefault(mod, set()).update(key_re.findall(body))

    # "gox" 聚合 = gox_module.go 里 submodules 清单的并集（不是"全部 gx/*"——
    # 清单是显式的，未来若刻意排除某模块，判据要跟着清单走）。
    gox_src = io.open(os.path.join(ROOT, "stdlib/gox_module.go"), encoding="utf-8").read()
    sm = re.search(r'submodules\s*:=\s*\[\]string\{(.*?)\}', gox_src, re.S)
    if not sm:
        print("!! stdlib/gox_module.go 里找不到 submodules 清单")
        sys.exit(1)
    subs = re.findall(r'"([^"]+)"', sm.group(1))
    union = set()
    for mod in subs:
        union |= exports.get(mod, set())
    exports["gox"] = union
    return exports, subs


# ---------- 2. 扫描目标文件 ----------
def scan_targets():
    targets = []
    for d in SCAN_DIRS:
        base = os.path.join(ROOT, d)
        for dirpath, dirnames, filenames in os.walk(base):
            dirnames[:] = [x for x in dirnames if x not in SKIP_DIRS]
            for fn in filenames:
                if os.path.splitext(fn)[1] in TEXT_EXT:
                    targets.append(os.path.join(dirpath, fn))
    for f in SCAN_FILES:
        p = os.path.join(ROOT, f)
        if os.path.isfile(p):
            targets.append(p)
    return sorted(targets)


# ---------- 3. 文件模块的 export 提取 ----------
export_re = re.compile(
    r'^\s*export\s+(?:\{([^}]*)\}'
    r'|(?:const|let|var|function\*?|class|interface|type|enum)\s+([A-Za-z_$][\w$]*)'
    r'|default\b)', re.M)


def file_export_names(src: str) -> set:
    have = set()
    for m in export_re.finditer(src):
        braces, single = m.groups()
        if braces:
            have.update(parse_braced_names(braces))
        elif single:
            have.add(single)
    return have


def main():
    list_only = "--list" in sys.argv
    exports, subs = collect_exports()

    if list_only:
        print("=== 内置模块导出表（真源 = Go 注册表） ===")
        for mod in sorted(exports):
            print("%-16s %2d: %s" % (mod, len(exports[mod]), ", ".join(sorted(exports[mod]))))
        return

    failures = []

    for path in scan_targets():
        rel = os.path.relpath(path, ROOT).replace("\\", "/")
        try:
            text = io.open(path, encoding="utf-8").read()
        except (UnicodeDecodeError, PermissionError):
            continue

        for m in import_stmt_re.finditer(text):
            names_part, star, default, spec = m.groups()
            line = text[:m.start()].count("\n") + 1

            if spec in exports:
                mod = spec
            elif spec.startswith("gx/") or spec == "gox":
                if spec not in ALLOW_UNKNOWN_MODULE:
                    failures.append(
                        "UNKNOWN-MODULE %-42s :%d  from %r —— 没有任何 Go 注册方注册这个模块"
                        % (rel, line, spec))
                continue
            else:
                continue  # 文件模块 / 相对导入走检查 2

            for name in sorted(parse_braced_names(names_part or "")):
                if name in exports[mod]:
                    continue
                if (rel, spec, name) in ALLOW_BUILTIN:
                    continue
                failures.append(
                    "MISSING %-44s :%d  import { %s } from %r —— 不在 %s 的导出表里"
                    % (rel, line, name, spec, spec))

        # 相对导入（仅脚本类文件）
        if os.path.splitext(path)[1] != ".md":
            rel_re = re.compile(
                r'import\s+(?:\{([^}]*)\}|([A-Za-z_$][\w$]*)|)\s*from\s*[\'"]'
                r'(\.{1,2}/[^\'"]+)[\'"]')
            for m in rel_re.finditer(text):
                names_part, default, spec = m.groups()
                line = text[:m.start()].count("\n") + 1
                target = os.path.normpath(os.path.join(os.path.dirname(path), spec))
                if not os.path.isfile(target):
                    failures.append(
                        "NOFILE    %-42s :%d  from %r —— 目标文件不存在" % (rel, line, spec))
                    continue
                try:
                    tgt_src = io.open(target, encoding="utf-8").read()
                except (UnicodeDecodeError, PermissionError):
                    continue
                have = file_export_names(tgt_src)
                for name in sorted(parse_braced_names(names_part or "")):
                    if name not in have:
                        failures.append(
                            "NOEXPORT %-43s :%d  import { %s } from %r —— 目标没有导出该名字"
                            % (rel, line, name, spec))

    if failures:
        print("check-imports: %d 处不一致" % len(failures))
        for f in failures:
            print("  " + f)
        print("真源：object.RegisterBuiltinModule 注册表 + 各文件的 export 声明；"
              "故意错误示例须登记进本脚本的 ALLOW_BUILTIN 并写明理由。")
        sys.exit(1)
    print("check-imports: OK（内置模块 import 与文件模块相对导入全部与导出表一致）")


if __name__ == "__main__":
    sys.stdout.reconfigure(encoding="utf-8")
    main()
