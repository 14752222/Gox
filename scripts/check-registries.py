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

3. **版本号一致**（r4yycE：三处手工同步，本项就是它的机器闸门）。
   `cmd/gox/main.go` 的 `const version`、`npm/package.json` 的 `version`、
   `npm/packages/*/package.json` 的子包 version 必须同号，以 `cmd/gox/main.go`
   为基准。每条来源都带**文件:行号**（官网页面里的版本号由 pages.yml 的
   `scripts/check-site.py` 那一步负责）。

4. **npm 包清单自洽**。主包 `package.json` 的 `bin` 指向真实存在的文件，`files`
   覆盖 `bin/`、`binaries/`，且**不含 `mobile/`**（历史教训：发过一个没有二进制的
   空壳包，本地 Windows 不复现、只有 Linux CI 上必现 —— 本地能查的只有清单自洽性）。

5. **导出名单 golden**（r3FFt6）。内置模块的导出名单落成
   `docs/exports.golden.json`（由 `scripts/gen-exports-golden.py` 生成），CI 用
   「实抽 vs golden」diff：改了导出面就必须重新生成并提交，让 API 变化在
   `git diff` 里现形，而不是混在一次大提交里被人漏看。

6. **移动端子包自洽**（M11）。`npm/packages/<dirname>/package.json` 是「平台 × ABI」
   子包的定义：命名、版本（须与主包同号）、`files` 清单必须自洽；若本机已跑过
   `build-npm-mobile.sh`（`dist/npm-mobile-pkgs/<dirname>/` 存在），再逐产物比对
   `manifest.json` 里的 sha256/size —— 这是"发出去的库是不是这次编的"唯一硬证据。

用法：
    python3 scripts/check-registries.py            # 跑全部检查
    python3 scripts/check-registries.py --list     # 只打印实际抽出的注册表
    python3 scripts/check-registries.py --version  # 只跑版本号那一类（发版闸门）
    python3 scripts/check-registries.py --github   # 失败时额外输出 ::error:: 注解

退出码非 0 表示失败。
"""
from __future__ import annotations

import importlib.util
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
                      "radio", "rating", "search", "separator", "slider", "spacer", "switch",
                      # video: 叶子控件（无流式子节点），但它**有** drawNode 分支
                      # （paintVideo 自己画封面/占位 + 装饰），所以只在这张表里。
                      "video"]
}
EXEMPT_LAYOUT.update({
    "#text": TEXT_NODE,
    "text": "文本容器在 default 分支里按内容区放置它的 #text 子节点",
    "tooltip-popup": ("弹层盒子由 positionTooltipPopup 显式定位（gfx/tooltip.go：按 placement"
                      "贴触发盒旁边，越界翻转 + clamp 进窗口），不走 layoutNode ——"
                      "内容是单行文字，绘制分支自画，没有子节点可排"),
    "table-header": ("行盒由 layoutTable 分配（gfx/table.go：Rect{area.X, y, area.W, tableRowH}），"
                     "单元格再按列宽摆开（layoutTableRowCells）——与自身固有尺寸无关"),
    "table-cell": ("格盒由 layoutTableRowCells 按列宽分配（gfx/table.go），"
                   "列宽来自 tableColumnWidths 的显式 width / 内容比例分配"),
    "menuitem": ("声明位置的 menuitem 不占位也不排布：文字由下拉弹层里物化出的 "
                 "menu-item 画。这里只需要 intrinsicSize 给一个名义行高，"
                 "防止它的子 menu（子菜单）被 drawNode 的空盒剪枝剪掉"),
    "rect": "通用装饰盒：子节点按内容区左上角叠放（default 分支）",
    "window": NOT_IN_TREE,
})

EXEMPT_INTRINSIC = {
    "dialog": ("盒子由 layoutDialog 取窗口盒（gfx/overlay.go：n.Box = n.windowBox()），"
               "子节点在窗口里居中 —— 与自身固有尺寸无关"),
    "drawer": ("弹层盒子由 layoutDrawer 取窗口盒（gfx/drawer.go：n.Box = n.windowBox()），"
               "内容卡片贴 side 边、宽按 width prop —— 与自身固有尺寸无关"),
    "menu-item": ("行盒由 layoutMenu 分配（gfx/menu.go：row.Box = Rect{…}），"
                  "固有尺寸不参与"),
    "menu-popup": POPUP_BOX + "（一级下拉挂在菜单标题下方，子菜单挂在触发项右侧）",
    "select-popup": POPUP_BOX + "（贴字段正下方且等宽，见 gfx/layout.go 的 select-popup 分支）",
    "datepicker-popup": (POPUP_BOX + "（贴字段正下方，尺寸由 datepickerPopupSize 按"
                        "7 格 × 行数 + 回显行算出，见 gfx/datepicker.go）"
                        "；弹层内容是整块自绘的（月份头/星期行/日期格都没有子节点），"
                        "所以固有尺寸不参与"),
    "colorpicker-popup": (POPUP_BOX + "（贴字段正下方，尺寸由 colorpickerPopupSize 按"
                          "色板行列数算出，见 gfx/colorpicker.go）"
                          "；弹层内容是整块自绘的，所以固有尺寸不参与"),
    "tooltip-popup": ("弹层盒子由 positionTooltipPopup 显式定位（gfx/tooltip.go：按 placement"
                      "贴触发盒旁边，越界翻转 + clamp 进窗口），不走 layoutNode ——"
                      "内容是单行文字，绘制分支自画，没有子节点可排"),
    "rect": "通用装饰盒：固有尺寸只认显式 width/height（default 分支），没有内容尺寸",
    "window": NOT_IN_TREE,
}

EXEMPT_RASTER = {t: CONTAINER for t in ["column", "grid", "row", "view"]}
EXEMPT_RASTER.update({
    "tooltip": "布局透明触发容器：走 default 的 paintBoxDecor（无 background/border 就不画，"
               "弹层 tooltip-popup 由自己的绘制分支画）",
    "table": "数据展示容器：自身只走 default 的 paintBoxDecor（背景/边框），"
             "网格与文字全由内部构造的 table-header / table-row / table-cell 各自绘制",
    "tree": "数据展示容器：自身只走 default 的 paintBoxDecor（背景/边框），"
            "行文字与展开箭头由内部构造的 tree-row 绘制",
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
    """返回 (版本号, 行号)。

    行号按**原始文件**算：strip_comments 后的文本是给正则用的，行数是否与原文件
    对齐不该成为本函数的隐式依赖 —— 一旦哪天改成"删注释行"，用剥离后的文本算
    出来的行号就会指向错的地方，而错行号比没有行号更糟（照着它去改会改错文件）。
    """
    raw = read(os.path.join("cmd", "gox", "main.go"))
    m = re.search(r'const version\s*=\s*"([^"]+)"', strip_comments(raw))
    if not m:
        raise Fail('cmd/gox/main.go 里找不到 `const version = "…"`')
    return m.group(1), line_of(raw, r'const version\s*=\s*"')


def line_of(text, pattern):
    """在 text 里找 pattern 首次命中的行号（1 起）。找不到返回 0。

    re.M 是必需的：pattern 里带 `^` 时，默认只匹配整个文本的开头 —— 那会让
    `"version"` 这类"不在第一行"的字段永远拿不到行号，输出里变成一堆 `?`。
    """
    m = re.search(pattern, text, re.M)
    if not m:
        return 0
    return text.count("\n", 0, m.start()) + 1


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
#
# 为什么每条来源都要带**文件:行号**：版本号靠人同步（r4yycE），发版时任何一处漏改
# 都只在事后才被发现。只报一句"版本号不一致"等于让人去全仓 grep —— 那正是
# "只给 exit code、不给归因"的老毛病（看板 rXrGfu）。所以这里把每一处真相点
# 的 文件:行号 = 值 全列出来，改错的是哪个文件一眼可见。
def version_sources():
    """收集版本号的所有真相点，返回 [(说明, 相对路径, 行号, 值), …]。

    顺序有意义：第一项是**基准**（cmd/gox/main.go —— 用户 `gox version` 读到的
    就是它），其余向它对齐。
    """
    go_ver, go_line = extract_version()
    srcs = [("cmd/gox/main.go 的 const version", os.path.join("cmd", "gox", "main.go"),
             go_line, go_ver)]

    pkg_rel = os.path.join("npm", "package.json")
    srcs.append(("npm/package.json 的 version", pkg_rel,
                 line_of(read(pkg_rel), r'^\s*"version"\s*:'),
                 str(load_pkg().get("version", ""))))

    # 平台子包（M11）：与主包成对发布，版本号必须同号 —— 壳工程与引擎对不上号
    # 是"装上就崩"级别的错，而它在 registry 上表现为两个包安静地共存。
    pkgs_dir = os.path.join(ROOT, "npm", "packages")
    if os.path.isdir(pkgs_dir):
        for name in sorted(os.listdir(pkgs_dir)):
            rel = os.path.join("npm", "packages", name, "package.json")
            p = os.path.join(ROOT, rel)
            if not os.path.isfile(p):
                continue
            with open(p, encoding="utf-8") as f:
                try:
                    sub = json.load(f)
                except ValueError as e:
                    raise Fail("%s 解析失败：%s" % (rel, e))
            srcs.append(("子包 %s 的 version" % name, rel,
                         line_of(read(rel), r'^\s*"version"\s*:'),
                         str(sub.get("version", ""))))
    return srcs


def check_version():
    srcs = version_sources()
    base_label, base_path, base_line, base_ver = srcs[0]

    # 通过时也把整张表打出来：版本号是**唯一**一个"对了也要留证据"的检查 ——
    # 发版记录里能直接抄到"这一版各处分别写的什么"，事后对账不用再翻文件。
    lines = ["  版本真相点（%d 处，以 %s 为基准）：" % (len(srcs), base_label)]
    for label, path, ln, val in srcs:
        lines.append("    %s:%s = %r   [%s]" % (path, ln or "?", val, label))

    diff = [(l, p, ln, v) for l, p, ln, v in srcs[1:] if v != base_ver]
    if not diff:
        print("\n".join(lines))
        return base_ver

    lines.append("")
    # 「版本号不一致」这五个字是 check-registries-selftest.py 的断言关键字，
    # 也是人一眼认出这是版本问题的锚点 —— 改写这条消息时要同步改自测。
    lines.append("  版本号不一致：%d 处与基准 %r 对不上（把它们改成 %r 即可）："
                 % (len(diff), base_ver, base_ver))
    for label, path, ln, val in diff:
        lines.append("    %s:%s 现在是 %r ≠ %r   [%s]" % (path, ln or "?", val, base_ver, label))
    lines.append("")
    lines.append("  后果：`gox version` 报的与实际发出去的包不是同一个版本号，"
                 "用户装到的包与命令行自报的身份对不上 —— 且只在事后才被发现。")
    raise Fail("\n".join(lines))


# ── 检查 6：导出名单 golden 与代码一致 (r3FFt6) ────────────────────────────
#
# 抽取逻辑只有一份，活在 scripts/gen-exports-golden.py 里（它反过来按文件加载
# 本脚本取真源）。golden 文件的意义见该脚本文件头：导出面是 API 契约，变化
# 必须被人看见，所以 CI 只做 diff、不自动刷新。
def check_exports_golden():
    helper = os.path.join(ROOT, "scripts", "gen-exports-golden.py")
    spec = importlib.util.spec_from_file_location("gen_exports_golden", helper)
    if spec is None or spec.loader is None:
        raise Fail("加载 %s 失败（脚本改名了？）" % helper)
    gen = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(gen)

    rel = os.path.join("docs", "exports.golden.json")
    path = os.path.join(ROOT, rel)
    if not os.path.isfile(path):
        raise Fail("%s 不存在 —— 先跑 python3 scripts/gen-exports-golden.py 生成它"
                   "（它是内置模块导出名单的唯一机器可读真相）" % rel)
    try:
        with open(path, encoding="utf-8") as f:
            golden = json.load(f)
    except (OSError, ValueError) as e:
        raise Fail("%s 读不出来：%s" % (rel, e))

    problems = gen.diff(golden, gen.current(sys.modules[__name__]))
    if problems:
        raise Fail("导出名单 golden 与代码不一致 —— 导出面变了要让人看见："
                   "确认无误后跑 python3 scripts/gen-exports-golden.py 重新生成，"
                   "并**随代码一起提交**（README / 官网 / 脚手架都要照它更新）\n"
                   + "\n".join(problems))
    return True


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

    if "mobile/" in pkg.get("files", []):
        problems.append(
            "主包 package.json 的 files 里出现了 mobile/ —— 移动端按「平台 × ABI」走独立"
            "子包（npm/packages/goxjs-mobile-*，M11 决策）；塞回主包会把每个桌面用户从"
            "~20MB 顶到 ~72MB。移动端产物请放进对应子包，不要放主包。")

    if problems:
        raise Fail("\n".join("  - " + p for p in problems))
    return pkg


# ── 检查 5：移动端子包自洽（M11）─────────────────────────────────────────────
# 平台 → 该子包应有的产物文件（除 manifest.json 外）
MOBILE_PLATFORM_FILES = {
    "android": ["libgox.so"],
    "harmony": ["libgox.so"],
    "ios": ["libgox.a", "libgox.h"],
}

# 本工作流只校验已知平台；出现新平台说明表要跟着改，所以未知平台也报错。
MOBILE_PLATFORMS = ("android", "harmony", "ios")


def _subpkg_platform(dirname):
    # dirname 形如 goxjs-mobile-<platform>-<abi>；platform 取第一段，其余是 abi。
    if not dirname.startswith("goxjs-mobile-"):
        return None, None
    rest = dirname[len("goxjs-mobile-"):]
    if "-" not in rest:
        return None, None
    platform, abi = rest.split("-", 1)
    return platform, abi


def check_mobile_subpackages():
    pkg = load_pkg()
    pkg_ver = pkg.get("version")
    base = os.path.join(ROOT, "npm", "packages")
    if not os.path.isdir(base):
        raise Fail("npm/packages/ 不存在 —— 移动端平台子包的定义目录（M11）不见了吗？")

    dirs = sorted(d for d in os.listdir(base)
                  if os.path.isdir(os.path.join(base, d)))
    if not dirs:
        raise Fail("npm/packages/ 下没有任何子包目录 —— M11 的平台子包定义丢了？")

    problems = []
    staged_root = os.path.join(ROOT, "dist", "npm-mobile-pkgs")
    checked_staged = 0

    for dirname in dirs:
        platform, abi = _subpkg_platform(dirname)
        if platform is None:
            problems.append("子包目录 %s 命名不符 goxjs-mobile-<platform>-<abi>" % dirname)
            continue
        if platform not in MOBILE_PLATFORMS:
            problems.append(
                "子包 %s 的平台 %r 不在已知表内（%s）—— 加了新平台要同步本脚本"
                % (dirname, platform, "/".join(MOBILE_PLATFORMS)))
            continue

        # 1) package.json 自洽
        pj = os.path.join(base, dirname, "package.json")
        if not os.path.isfile(pj):
            problems.append("子包 %s 缺 package.json" % dirname)
            continue
        try:
            with open(pj, encoding="utf-8") as f:
                sub = json.load(f)
        except (OSError, ValueError) as e:
            problems.append("子包 %s 的 package.json 读不了：%s" % (dirname, e))
            continue

        want_name = "@goxjs/" + dirname
        if sub.get("name") != want_name:
            problems.append("子包 %s 的 name=%r，应为 %r" % (dirname, sub.get("name"), want_name))
        if sub.get("version") != pkg_ver:
            problems.append(
                "子包 %s 的 version=%r 与主包 %r 不一致（壳工程↔引擎须成对发布）"
                % (dirname, sub.get("version"), pkg_ver))
        if (sub.get("publishConfig") or {}).get("access") != "public":
            problems.append("子包 %s 的 publishConfig.access 应为 public" % dirname)

        want_files = set(MOBILE_PLATFORM_FILES[platform]) | {"manifest.json"}
        got_files = set(sub.get("files") or [])
        if got_files != want_files:
            problems.append(
                "子包 %s 的 files=%s，应为 %s（平台 %s 的产物清单）"
                % (dirname, sorted(got_files), sorted(want_files), platform))

        # 2) 若本机有 staging 产物，逐产物比对 manifest 的 sha256/size
        stage = os.path.join(staged_root, dirname)
        mf = os.path.join(stage, "manifest.json")
        if not os.path.isfile(mf):
            continue
        try:
            with open(mf, encoding="utf-8") as f:
                man = json.load(f)
        except (OSError, ValueError) as e:
            problems.append("子包 %s 的 manifest.json 读不了：%s" % (dirname, e))
            continue

        if man.get("goxVersion") != pkg_ver:
            problems.append(
                "子包 %s 的 manifest.goxVersion=%r 与主包 %r 不一致"
                % (dirname, man.get("goxVersion"), pkg_ver))
        if man.get("platform") != platform or man.get("abi") != abi:
            problems.append(
                "子包 %s 的 manifest platform/abi=%r/%r 与目录名不符"
                % (dirname, man.get("platform"), man.get("abi")))

        listed = sorted(a.get("path") for a in man.get("artifacts", []))
        if listed != sorted(want_files - {"manifest.json"}):
            problems.append(
                "子包 %s 的 manifest 列了 %s，应为 %s"
                % (dirname, listed, sorted(want_files - {"manifest.json"})))

        for a in man.get("artifacts", []):
            rel = a.get("path")
            full = os.path.join(stage, rel)
            if not os.path.isfile(full):
                problems.append("子包 %s 的 manifest 列了 %s，但 staging 里没有" % (dirname, rel))
                continue
            want = a.get("sha256")
            got = _sha256(full)
            if got != want:
                problems.append(
                    "子包 %s/%s 的 sha256 不符（manifest=%s 实际=%s）"
                    % (dirname, rel, want, got))
            size = os.path.getsize(full)
            if a.get("size") != size:
                problems.append(
                    "子包 %s/%s 的 size 不符（manifest=%s 实际=%d）"
                    % (dirname, rel, a.get("size"), size))
        checked_staged += 1

    if problems:
        raise Fail("\n".join("  - " + p for p in problems))
    return "子包 %d 个（含 staging 校验 %d 个）" % (len(dirs), checked_staged)


def _sha256(path):
    import hashlib
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()



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
    argv = sys.argv[1:]
    if "--list" in argv:
        return cmd_list()

    # --github：把失败原因同时写成一行 ::error:: 注解。
    #   未登录读不到 job 日志，注解是唯一的归因入口（看板 rXrGfu 的教训）。
    #   stdout 上仍然保留完整的多行证据 —— 注解有长度上限，会截断，不能只靠它。
    github = "--github" in argv

    # --version：只跑版本号那一类。发版流水线用它做**专门一步**，失败时输出里
    #   只有版本信息，不会被组件/模块那几类的输出淹没。
    only_version = "--version" in argv

    checks = [
        ("内置 GUI 组件四处同步", check_components),
        ("内置模块三处同步", check_modules),
        ("版本号一致 (cmd/gox/main.go ↔ npm/package.json)", check_version),
        ("npm 包清单自洽", check_npm_manifest),
        ("移动端子包自洽 (M11)", check_mobile_subpackages),
        ("导出名单 golden ↔ 代码 (r3FFt6)", check_exports_golden),
    ]

    if only_version:
        checks = [c for c in checks if c[1] is check_version]
        if not checks:
            raise SystemExit("--version 没匹配到任何检查（脚本内部错误）")

    failed = 0
    for title, fn in checks:
        try:
            fn()
        except Fail as e:
            failed += 1
            print("FAIL  %s" % title)
            print(str(e))
            if github:
                # 注解里的换行会被 GitHub 压成一坨，用 ｜ 分隔可读性更好
                flat = " ｜ ".join(x.strip() for x in str(e).splitlines() if x.strip())
                print("::error::%s —— %s" % (title, flat[:2000]))
        else:
            print("ok    %s" % title)

    if failed:
        print("\n%d/%d 项检查失败。" % (failed, len(checks)))
        return 1
    print("\n全部 %d 项检查通过。" % len(checks))
    return 0


if __name__ == "__main__":
    sys.exit(main())
