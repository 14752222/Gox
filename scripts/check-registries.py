#!/usr/bin/env python3
"""Gox 注册表一致性检查 —— 本地与 CI 共用同一份判据。

背景：Gox 里有几张**必须手工同步**的注册表，漏改不会报错，只会静默失效
（组件渲染成空盒子、模块少一半导出、发出去的 npm 包版本号对不上、包里没有
二进制）。本脚本把这些静默失效变成 CI 红，并且每条失败都带证据。

四类检查：

1. **内置 GUI 组件四处同步**（gfx/）。一个脚本可写的标签按需出现在：
   - `gfx/node.go` 的 `knownTags`（决定 `h()` 是否对该标签告警）
   - `gfx/layout.go` 的 `layoutNode`（布局分派）
   - `gfx/layout.go` 的 `intrinsicSize`（固有尺寸）
   - `gfx/raster.go` 的 `drawNode`（绘制分派）

   四处**并非恒等**：叶子控件走 `layoutNode` 的 default 分支、容器走 `drawNode`
   的通用盒分支，都是有意的（理由逐条记在 `EXEMPT_*` 里）。所以判据不是
   "四处都要有"，而是"**代码与豁免表双向一致**"：
     * 表里说豁免、代码里却有 `case` ⇒ 豁免过期，报错（提醒删表项或删分支）
     * 表里没有、代码里也没有 `case` ⇒ 漏登记，报错并列出**缺哪个站点**
   这样"只改了一处就提交"必然在 CI 上现形，而不是等到画面上出现空盒子。

2. **内置模块三处同步**（gx/*）。`object.RegisterBuiltinModule("gx/xxx", …)`
   必须同时登记进 `stdlib/gox_module.go` 的 `submodules`（聚合入口 "gox" 的并集）；
   同一模块名不得重复注册（`init()` 顺序决定谁赢，后者静默覆盖前者）；
   不同子模块不得导出同名 API（`merged[k] = v` 是 last-wins，会静默吞掉前一个）。
   注：模块名走 `gx/` 前缀已被 `vm.loadModule` 的保留命名空间拦下，无需额外登记。

3. **版本号一致**。`main.go` 的 `const version` 必须等于 `npm/package.json` 的
   `version`（官网那四个页面里的版本号由 `scripts/check-site.py` 负责）。

4. **npm 包清单自洽**。`package.json` 的 `bin` 指向真实存在的文件，`files`
   覆盖 `bin/` 与 `binaries/`（历史上发过一个没有二进制的空壳包，本地 Windows
   不复现、只有 Linux CI 上必现 —— 本地能查的只有清单自洽性）。

用法：
    python3 scripts/check-registries.py            # 跑全部检查
    python3 scripts/check-registries.py --list     # 只打印实际抽出的注册表

退出码非 0 表示失败。
"""
from __future__ import annotations

import json
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# 扫描 Go 源码时跳过的目录。.mimosa / dist 里放着**源码快照**，扫进去会把
# 已经删掉的老标签当成"缺失"报出来，是纯噪音。
SKIP_DIRS = {".git", ".mimosa", ".workbuddy", ".idea", "dist", "node_modules", "vendor"}

# 出现在分派点、但不属于 knownTags 的内部标签（脚本写不到，由 Go 侧构造）。
# 加进来的每一个都要有理由，因为这意味着"用户在 h() 里写它会被告警，
# 但内核确实在处理它"。
INTERNAL_TAGS = {
    "slot": "动态子节点占位容器（viewNewSlot 造），布局透明，脚本不可写",
}

LEAF = ("叶子控件：没有流式子节点，走 layoutNode 的 default 分支"
        "（按自身 width/height 在内容区左上角定位，不做容器排布）")
CONTAINER = ("容器：走 drawNode 的 default 分支（paintBoxDecor 画自己的背景/边框"
             " + 递归子节点）")
POPUP_BOX = "弹层盒子由宿主分配（它贴在触发元素旁边，而不是按自身内容定尺寸）"
NOT_IN_TREE = "根元素：render() 里被拆成窗口配置，不进渲染树，不参与布局与绘制"
TEXT_NODE = "文本节点没有自己的盒子：位置由父节点在内容区放置（default 分支）"

# ── 内置组件豁免表 ────────────────────────────────────────────────────────────
# 结构：站点名 -> {标签: 理由}。**未出现在这里的 (标签, 站点) 一律要求代码里有分支。**
# 理由必须是真实的机制，不能写"以后补" —— 它会被原样塞进 CI 的 ::error:: 注解。
EXEMPT_LAYOUT = {
    t: LEAF for t in ["canvas", "checkbox", "image", "input", "progress",
                      "radio", "separator", "slider", "spacer", "switch"]
}
EXEMPT_LAYOUT.update({
    "#text": TEXT_NODE,
    "text": "文本容器在 default 分支里按内容区放置它的 #text 子节点",
    "menuitem": ("声明位置的 menuitem 不占位也不排布：文字由下拉弹层里物化出的 "
                 "menu-item 画。这里只需要 intrinsicSize 给一个名义行高，"
                 "防止它的子 menu（子菜单）被 drawNode 的空盒剪枝剪掉"),
    "rect": "通用装饰盒：子节点按内容区左上角叠放（default 分支）",
    "window": NOT_IN_TREE,
})

EXEMPT_INTRINSIC = {
    "dialog": ("盒子由 layoutDialog 取窗口盒（gfx/overlay.go：n.Box = n.windowBox()），"
               "子节点在窗口里居中 —— 与自身固有尺寸无关"),
    "menu-item": ("行盒由 layoutMenu 分配（gfx/menu.go：row.Box = Rect{…}），"
                  "固有尺寸不参与"),
    "menu-popup": POPUP_BOX + "（一级下拉挂在菜单标题下方，子菜单挂在触发项右侧）",
    "select-popup": POPUP_BOX + "（贴字段正下方且等宽，见 gfx/layout.go 的 select-popup 分支）",
    "rect": "通用装饰盒：固有尺寸只认显式 width/height（default 分支），没有内容尺寸",
    "window": NOT_IN_TREE,
}

EXEMPT_RASTER = {t: CONTAINER for t in ["column", "grid", "row", "view"]}
EXEMPT_RASTER.update({
    "button": "button 走 default 的 paintBoxDecor（装饰统一出口，交互反馈也挂在那里）",
    "menuitem": "声明位置不绘制：文字由下拉弹层里物化出的 menu-item 画",
    "rect": "通用装饰盒，走 default 的 paintBoxDecor",
    "select-popup": "弹层自身不绘制，逐项由 select-option 各自绘制",
    "window": NOT_IN_TREE,
})

# 站点名 -> (文件, 豁免表变量名)
COMPONENT_SITES = {
    "layoutNode": ("gfx/layout.go", "EXEMPT_LAYOUT"),
    "intrinsicSize": ("gfx/layout.go", "EXEMPT_INTRINSIC"),
    "drawNode": ("gfx/raster.go", "EXEMPT_RASTER"),
}


class Fail(Exception):
    """一条检查失败。message 会带证据，直接进 CI 注解。"""


def read(rel):
    p = os.path.join(ROOT, rel)
    if not os.path.isfile(p):
        raise Fail("找不到文件 %s（本检查的锚点，改名了就要同步改这里）" % rel)
    with open(p, encoding="utf-8") as f:
        return f.read()


def strip_comments(src):
    """去掉注释，**原样保留字符串字面量**（含反引号原始串）。

    不能用正则糊：源码里有 "git+https://github.com/…" 这样的字符串，简单地把
    `//…` 切掉会把字符串截断，进而把花括号配平搞崩 —— 那种"解析器悄悄算错"
    正是本脚本要防的静默失效，不能自己犯。
    """
    out = []
    i, n = 0, len(src)
    while i < n:
        c = src[i]
        if c in ('"', "'", "`"):
            quote = c
            out.append(c)
            i += 1
            while i < n:
                if quote != "`" and src[i] == "\\":
                    out.append(src[i:i + 2])
                    i += 2
                    continue
                out.append(src[i])
                if src[i] == quote:
                    i += 1
                    break
                i += 1
            continue
        if c == "/" and i + 1 < n and src[i + 1] == "/":
            while i < n and src[i] != "\n":
                i += 1
            continue
        if c == "/" and i + 1 < n and src[i + 1] == "*":
            i += 2
            while i + 1 < n and not (src[i] == "*" and src[i + 1] == "/"):
                i += 1
            i += 2
            continue
        out.append(c)
        i += 1
    return "".join(out)


def brace_block(src, open_idx):
    """从 open_idx 处的 '{' 开始，返回配对到 '}' 的内容（不含两端花括号）。"""
    if src[open_idx] != "{":
        raise Fail("内部错误：brace_block 起点不是 '{'，实际是 %r"
                   % src[open_idx:open_idx + 1])
    depth = 0
    for i in range(open_idx, len(src)):
        if src[i] == "{":
            depth += 1
        elif src[i] == "}":
            depth -= 1
            if depth == 0:
                return src[open_idx + 1:i], i
    raise Fail("大括号不配平：源码被截断或解析出错（起点偏移 %d）" % open_idx)


def func_body(src, sig):
    """取函数体（sig 形如 'func drawNode('）。"""
    try:
        i = src.index(sig)
    except ValueError:
        raise Fail("找不到函数 %s —— 本检查的锚点，函数改名/挪走了就要同步改这里" % sig)
    return brace_block(src, src.index("{", i))[0]


def switch_cases(body, anchor="switch n.Tag {"):
    """取 body 内第一个 `switch n.Tag {` 的全部分支标签。"""
    i = body.find(anchor)
    if i < 0:
        raise Fail("函数体里找不到 %r —— 分派点的写法变了，检查脚本要跟着改" % anchor)
    block, _ = brace_block(body, i + len(anchor) - 1)
    tags = set()
    for m in re.finditer(r"\bcase\b([^:]*):", block, flags=re.S):
        tags.update(re.findall(r'"([^"]+)"', m.group(1)))
    if not tags:
        raise Fail("%r 里一个 case 标签都没抽到 —— 解析逻辑失效，不要让它静默通过" % anchor)
    return tags


def tag_equalities(body):
    """取 body 里 `n.Tag == "x"` / `n.Tag != "x"` 形式的标签（非 switch 的特判分支）。"""
    return set(re.findall(r'n\.Tag\s*[!=]=\s*"([^"]+)"', body))


def extract_known_tags():
    src = strip_comments(read("gfx/node.go"))
    i = src.find("var knownTags")
    j = src.find("map[string]struct{}", i)
    if i < 0 or j < 0:
        raise Fail("gfx/node.go 里找不到 `var knownTags = map[string]struct{}{…}`")
    block, _ = brace_block(src, src.index("{", j + len("map[string]struct{}")))
    tags = set(re.findall(r'"([^"]+)"', block))
    if not tags:
        raise Fail("knownTags 抽出来是空的 —— 字面量写法变了，检查脚本要跟着改")
    return tags


def extract_site_tags():
    """返回 {站点名: 标签集合}。"""
    layout = strip_comments(read("gfx/layout.go"))
    raster = strip_comments(read("gfx/raster.go"))

    bodies = {
        "layoutNode": func_body(layout, "func layoutNode("),
        "intrinsicSize": func_body(layout, "func (n *GuiNode) intrinsicSize()"),
        "drawNode": func_body(raster, "func drawNode("),
    }
    return {site: switch_cases(b) | tag_equalities(b) for site, b in bodies.items()}


def go_files():
    for dirpath, dirnames, filenames in os.walk(ROOT):
        dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS]
        for fn in filenames:
            if fn.endswith(".go") and not fn.endswith("_test.go"):
                full = os.path.join(dirpath, fn)
                yield os.path.relpath(full, ROOT).replace("\\", "/"), full


REGISTER_RE = re.compile(r'RegisterBuiltinModule\(\s*"([^"]+)"')


def extract_registrations():
    """返回 {模块名: [相对路径, …]}（同一个名字可能出现多次）。"""
    reg = {}
    for rel, full in sorted(go_files()):
        with open(full, encoding="utf-8") as f:
            src = strip_comments(f.read())
        for m in REGISTER_RE.finditer(src):
            reg.setdefault(m.group(1), []).append(rel)
    if not reg:
        raise Fail("一个 RegisterBuiltinModule 都没找到 —— 注册写法变了，检查脚本要跟着改")
    return reg


def map_literal_keys(src, start):
    """取 src[start:] 里第一个 map 字面量的**顶层键**。

    只看 depth==0 且后面紧跟冒号的字符串 —— 否则会把值表达式里的字符串
    （`object.NewBuiltin("h", …)` 里的 "h"）误当成导出名。
    """
    mi = src.find("map[string]object.Value{", start)
    if mi < 0:
        return None
    block, _ = brace_block(src, src.index("{", mi + len("map[string]object.Value")))
    keys = set()
    depth = 0
    for m in re.finditer(r'"(?:[^"\\]|\\.)*"|[{}]', block):
        tok = m.group(0)
        if tok == "{":
            depth += 1
        elif tok == "}":
            depth -= 1
        elif depth == 0 and block[m.end():].lstrip()[:1] == ":":
            keys.add(tok[1:-1])
    return keys


# 导出表不是静态字面量的模块。**加进来必须写清理由** —— 这等于放弃对它的
# 同名导出检查，所以只允许"它的导出本来就是运行时算出来的"这种情况。
EXPORTS_NOT_STATIC = {
    "gox": '聚合入口的导出表是运行时并集（merged），不是字面量；它本身不实现 API',
}


def extract_module_exports():
    """返回 {模块名: 导出键集合}（跳过 EXPORTS_NOT_STATIC 里登记的）。"""
    out = {}
    for rel, full in sorted(go_files()):
        with open(full, encoding="utf-8") as f:
            src = strip_comments(f.read())
        for m in REGISTER_RE.finditer(src):
            name = m.group(1)
            if name in EXPORTS_NOT_STATIC:
                continue
            keys = map_literal_keys(src, m.end())
            if not keys:
                raise Fail("模块 %s（%s）抽不出导出名 —— 返回表可能不是 "
                           "map[string]object.Value 字面量了。本检查不能静默通过："
                           "要么把它的导出改回字面量，要么登记进 EXPORTS_NOT_STATIC "
                           "并写清理由（那等于放弃对它的同名导出检查）" % (name, rel))
            out[name] = keys
    return out


def extract_umbrella_submodules():
    src = strip_comments(read("stdlib/gox_module.go"))
    i = src.find("submodules := []string{")
    if i < 0:
        raise Fail("stdlib/gox_module.go 里找不到 `submodules := []string{…}`"
                   " —— 聚合入口的写法变了，检查脚本要跟着改")
    block, _ = brace_block(src, src.index("{", i))
    names = re.findall(r'"([^"]+)"', block)
    if not names:
        raise Fail("submodules 抽出来是空的 —— 解析失效，不要静默通过")
    return names


def extract_version():
    src = strip_comments(read("main.go"))
    m = re.search(r'const version\s*=\s*"([^"]+)"', src)
    if not m:
        raise Fail('main.go 里找不到 `const version = "…"`')
    return m.group(1)


def load_pkg():
    # npm/ 2026-09-24 起是独立仓库（gox-npm）的子模块：裸 clone 时这里会缺文件，
    # 所以先给一条能直接照做的提示，而不是把 FileNotFoundError 包成一句"读取失败"。
    path = os.path.join(ROOT, "npm", "package.json")
    if not os.path.exists(path):
        raise Fail(
            "npm/package.json 不存在 —— npm/ 现在是独立仓库的子模块，"
            "请先跑 git submodule update --init"
            "（克隆时用 git clone --recurse-submodules）"
        )
    try:
        with open(path, encoding="utf-8") as f:
            return json.load(f)
    except (OSError, ValueError) as e:
        raise Fail("读 npm/package.json 失败：%s" % e)


# ── 检查 1：内置组件四处同步 ─────────────────────────────────────────────────
def check_components():
    problems = []
    known = extract_known_tags()
    sites = extract_site_tags()
    exempt = {
        "layoutNode": EXEMPT_LAYOUT,
        "intrinsicSize": EXEMPT_INTRINSIC,
        "drawNode": EXEMPT_RASTER,
    }

    for tag in sorted(known):
        missing = []
        for site, (fname, table) in COMPONENT_SITES.items():
            present = tag in sites[site]
            if tag in exempt[site]:
                if present:
                    problems.append(
                        "%s：%s 里说有意不为 <%s> 写分支，但代码里存在该分支。"
                        "要么删掉 %s 的这一项（说明现在确实需要它了），"
                        "要么删掉 %s 里的这个 case"
                        % (fname, table, tag, table, site))
                elif not exempt[site][tag].strip():
                    problems.append("%s：%s[%r] 的豁免理由是空的" % (fname, table, tag))
            elif not present:
                missing.append(site)
        if missing:
            listed = "、".join("%s（%s，豁免表 %s）"
                            % (s, COMPONENT_SITES[s][0], COMPONENT_SITES[s][1])
                            for s in missing)
            problems.append(
                "knownTags 里的 <%s> 在以下站点既没有分支、也没登记豁免：%s。"
                "漏改站点 = 静默失效（组件被渲染成空盒子且不报错）。"
                "确认过确实有意不做的话，把它加进对应豁免表并写清机制"
                % (tag, listed))

    # 反方向：分派点里出现、但既不在 knownTags、也不是内部标签
    for site, (fname, _) in COMPONENT_SITES.items():
        for tag in sorted(sites[site]):
            if tag in known or tag in INTERNAL_TAGS:
                continue
            problems.append(
                "%s 的 %s 里有 case %r，但它既不在 gfx/node.go 的 knownTags 里，"
                "也不是已登记的内部标签。后果：脚本写 <%s> 会被 h() 误报为未知标签。"
                "要么登记进 knownTags，要么加进 INTERNAL_TAGS 并说明它是内部构造的"
                % (fname, site, tag, tag))

    # 清掉没人再用的豁免项，避免豁免表腐烂成噪音
    for site, (fname, table) in COMPONENT_SITES.items():
        for tag, reason in sorted(exempt[site].items()):
            if tag not in known:
                problems.append("%s：%s 里的 %r 不在 knownTags 里，是个僵尸条目"
                                % (fname, table, tag))
            elif not reason or not reason.strip():
                problems.append("%s：%s[%r] 没写豁免理由" % (fname, table, tag))

    if problems:
        raise Fail("\n".join("  - " + p for p in problems))
    return known, sites


# ── 检查 2：内置模块三处同步 ─────────────────────────────────────────────────
UMBRELLA_EXEMPT = {
    "gox": "聚合入口自身，不需要出现在自己的 submodules 列表里",
}


def check_modules():
    problems = []
    reg = extract_registrations()
    subs = extract_umbrella_submodules()
    exports = extract_module_exports()

    for name, files in sorted(reg.items()):
        if len(files) > 1:
            problems.append(
                "模块 %s 被注册了 %d 次：%s。RegisterBuiltinModule 是 last-wins，"
                "后者静默覆盖前者，哪一次生效取决于 init() 顺序"
                % (name, len(files), "、".join(files)))
        if name in UMBRELLA_EXEMPT:
            continue
        if name.startswith("gx/") and name not in subs:
            problems.append(
                "%s 在 %s 里注册了，但没进 stdlib/gox_module.go 的 submodules —— "
                'import { … } from "gox" 拿不到它的导出；细粒度 import 仍是好的，'
                "所以症状很隐蔽" % (name, files[0]))

    for name in subs:
        if name not in reg:
            problems.append(
                "stdlib/gox_module.go 的 submodules 列了 %s，但仓库里没有任何 "
                'RegisterBuiltinModule("%s", …) —— 聚合入口里这一项永远 Lookup 不到，'
                "静默跳过" % (name, name))

    # 跨子模块同名导出：gox 聚合是 merged[k] = v（last-wins），同名会静默吞掉前一个
    seen = {}
    for name in subs:
        if name not in exports:
            continue
        for key in sorted(exports[name]):
            if key in seen and seen[key] != name:
                problems.append(
                    "导出名 %r 同时被 %s 与 %s 导出。聚合入口 gox 的 merged[k]=v 是 "
                    "last-wins，先注册的那个会被静默吞掉；细粒度 import 各拿各的，"
                    "所以只在用聚合入口时才出问题" % (key, seen[key], name))
            else:
                seen.setdefault(key, name)

    if problems:
        raise Fail("\n".join("  - " + p for p in problems))
    return reg, subs, exports


# ── 检查 3：版本号一致 ──────────────────────────────────────────────────────
def check_version():
    go_ver = extract_version()
    pkg_ver = str(load_pkg().get("version", ""))
    if go_ver != pkg_ver:
        raise Fail("版本号不一致：main.go 的 const version = %r，"
                   "npm/package.json 的 version = %r。"
                   "`gox version` 报的与实际发出去的包不是同一个版本号"
                   % (go_ver, pkg_ver))
    return go_ver


# ── 检查 4：npm 包清单自洽 ──────────────────────────────────────────────────
def check_npm_manifest():
    pkg = load_pkg()
    problems = []

    for key, rel in sorted(pkg.get("bin", {}).items()):
        if not os.path.isfile(os.path.join(ROOT, "npm", rel)):
            problems.append("package.json 的 bin.%s 指向 %s，但该文件不存在" % (key, rel))

    if "binaries/" not in pkg.get("files", []):
        problems.append(
            "package.json 的 files 里没有 binaries/ —— 打出来的包里就没有二进制，"
            "用户装到的是一个空壳包（本地 Windows 不复现，Linux CI 上必现）")

    if problems:
        raise Fail("\n".join("  - " + p for p in problems))
    return pkg


def cmd_list():
    known = extract_known_tags()
    sites = extract_site_tags()
    reg = extract_registrations()
    subs = extract_umbrella_submodules()
    exports = extract_module_exports()
    print("knownTags (%d)：%s" % (len(known), " ".join(sorted(known))))
    for site in COMPONENT_SITES:
        print("%-14s (%2d)：%s" % (site, len(sites[site]), " ".join(sorted(sites[site]))))
    print("注册的模块 (%d)：%s" % (len(reg), " ".join(sorted(reg))))
    print("submodules (%d)：%s" % (len(subs), " ".join(subs)))
    for name in sorted(exports):
        print("  %-16s (%2d) %s" % (name, len(exports[name]), " ".join(sorted(exports[name]))))
    return 0


def main():
    if "--list" in sys.argv:
        return cmd_list()

    checks = [
        ("内置 GUI 组件四处同步", check_components),
        ("内置模块三处同步", check_modules),
        ("版本号一致 (main.go ↔ npm/package.json)", check_version),
        ("npm 包清单自洽", check_npm_manifest),
    ]

    failed = 0
    for title, fn in checks:
        try:
            fn()
        except Fail as e:
            failed += 1
            print("FAIL  %s" % title)
            print(str(e))
        else:
            print("ok    %s" % title)

    if failed:
        print("\n%d/%d 项检查失败。" % (failed, len(checks)))
        return 1
    print("\n全部 %d 项检查通过。" % len(checks))
    return 0


if __name__ == "__main__":
    sys.exit(main())
