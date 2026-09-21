#!/usr/bin/env python3
"""官网(website/)静态检查 —— 本地与 CI 共用同一份判据。

四类检查,每一条都对应一种"改着改着就乱掉"的真实故障:

1. **HTML 标签配对**:未闭合 / 错配的标签 (改文档时最容易漏一个 </div>)。
2. **版本号一致**:四个页面导航里的 `Gox vX.Y.Z` 必须彼此相等,且等于
   `npm/package.json` 的 version —— 官网版本号长期停在 v0.1.1 而包已经到 0.4.3,
   就是没有这条检查的后果。发版时改 package.json 忘改官网,这里会报出来。
3. **内部链接可达**:`href` / `src` 里指向本地的路径必须真实存在 (锚点会剥掉再查),
   避免目录写"见 §7"而页面上根本没有那个锚点。
4. **锚点存在**:`xxx.html#yyy` 里的 yyy 必须在目标页面里有 `id="yyy"`。

用法:
    python3 scripts/check-site.py            # 检查 website/ 全部页面
    python3 scripts/check-site.py file.html  # 只检查指定文件(仍做结构检查)

退出码非 0 表示失败;每条失败都带**具体文件与行号/片段**,方便直接定位。
"""
from __future__ import annotations

import json
import os
import re
import sys
from html.parser import HTMLParser

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SITE = os.path.join(ROOT, "website")
PKG = os.path.join(ROOT, "npm", "package.json")

VOID = {
    "area", "base", "br", "col", "embed", "hr", "img", "input",
    "link", "meta", "param", "source", "track", "wbr",
}

# 明确不允许再出现在官网上的旧内容 —— 出现即说明有人拿旧快照改了。
FORBIDDEN = [
    ("v0.1.1", "旧版本号:官网已到当前版本,这里还写着 v0.1.1"),
    ("go run ./dbgtool", "该命令对应的目录在仓库里不存在"),
]


class Page(HTMLParser):
    """一次解析同时收齐:标签栈、行内链接、出现的 id。"""

    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.stack: list[tuple[str, int]] = []
        self.problems: list[str] = []
        self.links: list[tuple[str, str, int]] = []  # (attr, value, line)
        self.ids: set[str] = set()
        self.text_chunks: list[str] = []

    # ---- 标签配对 ----
    def handle_starttag(self, tag, attrs) -> None:
        line = self.getpos()[0]
        d = dict(attrs)
        if "id" in d and d["id"]:
            self.ids.add(d["id"])
        for a in ("href", "src"):
            if d.get(a):
                self.links.append((a, d[a], line))
        if tag not in VOID:
            self.stack.append((tag, line))

    def handle_startendtag(self, tag, attrs) -> None:
        # 自闭合写法(<x/>):也算一次"出现过的 id/href",但不进栈
        d = dict(attrs)
        if "id" in d and d["id"]:
            self.ids.add(d["id"])
        for a in ("href", "src"):
            if d.get(a):
                self.links.append((a, d[a], self.getpos()[0]))

    def handle_endtag(self, tag) -> None:
        if tag in VOID:
            return
        line = self.getpos()[0]
        if not self.stack:
            self.problems.append(f"line {line}: 多余的 </{tag}>")
            return
        top, _ = self.stack[-1]
        if top == tag:
            self.stack.pop()
            return
        for i in range(len(self.stack) - 1, -1, -1):
            if self.stack[i][0] == tag:
                unclosed = [f"<{t}>(line {l})" for t, l in self.stack[i + 1:]]
                self.problems.append(
                    f"line {line}: </{tag}> 之前有未闭合的 {', '.join(unclosed)}")
                del self.stack[i:]
                return
        self.problems.append(f"line {line}: </{tag}> 没有对应的开始标签")

    def handle_data(self, data) -> None:
        self.text_chunks.append(data)

    def finish(self) -> list[str]:
        out = list(self.problems)
        for tag, line in self.stack:
            out.append(f"文件结束时 <{tag}>(line {line}) 仍未闭合")
        return out


def read(path: str) -> str:
    with open(path, encoding="utf-8") as f:
        return f.read()


def parse(path: str) -> Page:
    p = Page()
    p.feed(read(path))
    p.close()
    return p


def pages(extra: list[str]) -> list[str]:
    if extra:
        return [os.path.abspath(x) for x in extra]
    return sorted(
        os.path.join(SITE, n)
        for n in os.listdir(SITE)
        if n.endswith(".html")
    )


def pkg_version() -> str:
    if not os.path.exists(PKG):
        return ""
    try:
        with open(PKG, encoding="utf-8") as f:
            return str(json.load(f).get("version", ""))
    except (OSError, ValueError):
        return ""


def main(argv: list[str]) -> int:
    targets = pages(argv)
    if not targets:
        print("没有找到任何页面")
        return 1

    print("== 1/4 HTML 标签配对 ==")
    parsed: dict[str, Page] = {}
    bad = False
    for path in targets:
        p = parse(path)
        parsed[path] = p
        problems = p.finish()
        rel = os.path.relpath(path, ROOT).replace("\\", "/")
        if problems:
            bad = True
            print(f"[FAIL] {rel}")
            for x in problems[:12]:
                print(f"       {x}")
                print(f"::error file={rel}::{x}")
        else:
            print(f"[ok]   {rel}  ({len(read(path))} bytes)")

    print("== 2/4 版本号一致 ==")
    want = pkg_version()
    found: dict[str, set[str]] = {}
    for path, p in parsed.items():
        rel = os.path.relpath(path, ROOT).replace("\\", "/")
        vers = set(re.findall(r'class="ver">v([0-9][^<\s]*)<', read(path)))
        found[rel] = vers
        if not vers:
            bad = True
            print(f"[FAIL] {rel}: 导航里没有版本号徽章 (<span class=\"ver\">)")
            print(f"::error file={rel}::导航缺失版本号徽章")
    if not bad:
        allvers = set().union(*found.values()) if found else set()
        print(f"       页面版本: {sorted(allvers) or '—'}")
        print(f"       package.json: {want or '—'}")
        if len(allvers) != 1:
            bad = True
            for rel, v in sorted(found.items()):
                print(f"[FAIL] {rel}: 版本 {sorted(v)}")
                print(f"::error file={rel}::四个页面版本号不一致: {sorted(v)}")
        elif want and allvers != {want}:
            bad = True
            print(f"[FAIL] 官网版本 {sorted(allvers)} != npm/package.json 的 {want}")
            for rel in sorted(found):
                print(f"::error file={rel}::官网版本 {sorted(allvers)} 与 npm/package.json 的 {want} 不一致")
        else:
            print("[ok]   四个页面与 npm/package.json 版本一致")

    print("== 3/4 内部链接可达 ==")
    link_bad = False
    for path, p in parsed.items():
        rel = os.path.relpath(path, ROOT).replace("\\", "/")
        for attr, val, line in p.links:
            if re.match(r"^(https?:|mailto:|javascript:|data:|//)", val):
                continue
            target, _, frag = val.partition("#")
            if not target:
                continue  # 纯锚点,交给第 4 条
            target = target.split("?", 1)[0]
            resolved = os.path.normpath(os.path.join(os.path.dirname(path), target))
            if not os.path.exists(resolved):
                link_bad = bad = True
                msg = f"line {line}: {attr}=\"{val}\" 指向的文件不存在"
                print(f"[FAIL] {rel}: {msg}")
                print(f"::error file={rel}::{msg}")
    if not link_bad:
        print("[ok]   所有本地 href/src 都能解析到真实文件")

    print("== 4/4 锚点与旧内容 ==")
    for path, p in parsed.items():
        rel = os.path.relpath(path, ROOT).replace("\\", "/")
        for attr, val, line in p.links:
            if re.match(r"^(https?:|mailto:|javascript:|data:|//)", val):
                continue
            target, sep, frag = val.partition("#")
            if not sep or not frag:
                continue
            if target:
                resolved = os.path.normpath(os.path.join(os.path.dirname(path), target))
                if not os.path.exists(resolved):
                    continue  # 第 3 条已经报过
                try:
                    other = parsed.get(resolved) or parse(resolved)
                except OSError:
                    continue
                where = f"{target}#{frag} 的目标页"
            else:
                # 同页锚点:目录里写"见 §7",页面上就得真有那个 id
                other, where = p, "本页"
            if frag not in other.ids:
                bad = True
                msg = f'line {line}: {val} —— {where}里没有 id="{frag}"'
                print(f"[FAIL] {rel}: {msg}")
                print(f"::error file={rel}::{msg}")
        text = read(path)
        for needle, why in FORBIDDEN:
            if needle in text:
                bad = True
                print(f"[FAIL] {rel}: 出现旧内容 {needle!r} —— {why}")
                print(f"::error file={rel}::出现旧内容 {needle!r}: {why}")
    if not bad:
        print("[ok]   锚点全部命中,无旧内容残留")

    print()
    print("检查未通过" if bad else "官网检查全部通过")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
