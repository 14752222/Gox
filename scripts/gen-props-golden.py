#!/usr/bin/env python3
"""内置元素属性/事件白名单的 golden —— 生成与比对（rgQsDD）。

背景：gfx 的 `wireProp` 兜底是 `n.Props[name] = val`（gfx/node.go）—— 任何键名
都会静默落进 map，既没有校验也没有人读它。按 DOM 直觉写 `<button label="确定">`
得到的是一个 16px 宽的空白壳，控制台一声不响；写了内核还没实现的事件，回调
永远不触发，也是一声不响。用户会以为是自己脚本写错了 —— 比报错更伤采纳。

要拦它就得先有一张"内核认得什么"的全表：约 60 个标签 × 各自认识的 prop 与事件。
**手抄必然错**，抽错就误报，而误报会让人直接把整个开关关掉 —— 那比不做更糟。
所以抽取走 **Go AST**（tools/propdump，go/ast + 调用图），本脚本只负责
**合成与比对**：把实抽结果与几张手工登记表合成 golden，并生成给 gfx 用的
Go 源码。

## 抽取口径（tools/propdump 里的三条硬规则）

1. **只统计读点。** `n.Props[name] = val` 这种**写入**不算"内核认这个 prop" ——
   它只是把值存下来。判据是赋值语句的 LHS 检测。
2. **读点要归到标签**，靠调用图从"按 tag 分派"的根函数往回推。
   读的是**别人的节点**（`for _, c := range n.Children { c.PropNum("flexGrow") }`）
   一律算通用 —— 那种读点归属的标签是"子节点的标签"，判不出来。
3. **判不出来就当通用**。过宽只是漏报（少警告一次），过窄才是误报（警告一个
   真的 prop）—— 误报是这个开关的头号死因，宁可少报。

## 事件单独一张表

事件名散落在各派发点（`callHandler` / `callHandlerWithPoint` / `handlerInChain`），
抽法同上。额外的维度是 **status**：
  * `universalEvents` / `eventsByTag` —— 内核有派发点，写了一定会被调用；
  * `eventsNotImplemented` —— 文档/示例里承诺过，但内核**没有派发点**。这一档
    的报错文案必须明说"内核还没实现"，而不是笼统的"未知属性" —— 否则用户会
    去查自己脚本的拼写，查一天也查不出来。

`eventsNotImplemented` 是**手工登记**（每条带理由）：没有任何静态判据能区分
"计划中的事件"与"从来不存在的事件"。它随 golden 一起生成，同样被 CI 的 diff 兜着。

用法：
    python3 scripts/gen-props-golden.py            # 重新生成（白名单变更后跑）
    python3 scripts/gen-props-golden.py --check    # 只比对，不写（CI 用）
    python3 scripts/gen-props-golden.py --print    # 打到 stdout，不写文件

退出码：--check 下不一致为 1，一致为 0。
"""
from __future__ import annotations

import json
import os
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
GOLDEN_REL = os.path.join("docs", "props.golden.json")
GOFILE_REL = os.path.join("gfx", "knownprops_gen.go")
PROPDUMP_REL = os.path.join("tools", "propdump")

# ── 手工登记区（每条都要有理由，理由写进 golden 与生成的 Go 注释）────────────

# 指令 / 框架键：不进节点的 Props 读取路径，而是在 h() 里被指令层先消费掉
# （directive.go 的 elementDirectives、view.go 的 each/key、model.go 的 model）。
# 脚本写它们是**正确的用法**，不能报未知属性。
FRAMEWORK_PROPS = {
    "each": "each 指令：列表展开（gfx/directive.go / gfx/view.go）",
    "show": "show 指令：条件渲染（gfx/directive.go）",
    "fallback": "each 指令的空列表兜底（gfx/view.go）",
    "key": "each 指令的项标识：取值函数或字段名简写（gfx/view.go）",
    "stable": "each 指令的稳定复用开关（gfx/directive.go）",
    "model": "model= 双向绑定：展开成该标签的受控 prop（gfx/model.go）",
}

# 属性名前缀：由专门的一层校验，不进本白名单。
PROP_PREFIX_EXEMPT = {
    "aria-": "无障碍属性：由 gfx/a11y.go 的 a11yCheckProps 单独校验名字与取值",
    "data-": "用户自定义数据：内核不读，但也不该被当成笔误",
    "__": "内核内部键（如 __routeTo / _zebra）：由内核自己写，脚本不该手写",
}

# 事件名豁免：不是事件回调，而是"响应式函数 prop"（走 effect 求值）。
EVENT_EXEMPT = {
    "onDraw": "响应式绘制函数（wireDraw），不是事件回调（gfx/node.go）",
}

# 内核**还没有派发点**、但已经对外承诺过的事件。写了它们不能报"未知属性" ——
# 那会让人去查自己脚本的拼写 —— 必须明说"内核还没实现"。
# 新增/删除都要带着理由改这里，CI 的 golden diff 会盯住它。
EVENTS_NOT_IMPLEMENTED = {
    "onMouseLeave": "docs/gui-patterns.md:309 把 <button onMouseLeave={…}> 当示例写，"
                    "平台层有 EventMouseLeave，但 gfx 没有派发点（没有 callHandler）"
                    "—— 回调永远不会被调用。",
}

# 具体提示：这些不是"通用未知属性"，而是**有明确替代写法**的常见误解。
# 键是 "标签|属性"（"*" = 任意标签）。文案要给出替代写法，否则等于没说。
# 每条都来自实扫证据（见 commit message 的误报核对一节）。
PROP_HINTS = {
    "button|label": "button 不认 label —— 按钮文字要写成子节点 "
                    "<button>确定</button>（label 只在 menu / menubar / menuitem 上是真属性）",
    "avatar|round": "avatar 不认 round —— 要方的写 shape=\"square\"（缺省就是圆）",
    "row|align": "row 不认 align —— 交叉轴对齐用 alignItems，主轴用 justifyContent",
    "column|align": "column 不认 align —— 交叉轴对齐用 alignItems，主轴用 justifyContent",
    "table|borderless": "borderless 由 table-row / table-cell 自己读，写在 <table> 上不生效",
    "table|zebra": "table 不认 zebra —— 斑马纹由内核按行号自动写 _zebra，无法用 prop 开关",
}


def run_propdump():
    """跑 Go AST 抽取器，返回它打到 stdout 的 JSON。

    抽取逻辑**只写一份**（在 tools/propdump）：golden 与 gfx 用的是同一批数据，
    若两边各写一套正则，迟早漂移 —— 那时要查的就不是"白名单变了"，而是"哪个
    抽取器错了"，正是本单要消灭的那类噪音。
    """
    cmd = ["go", "run", "./" + PROPDUMP_REL.replace(os.sep, "/")]
    try:
        out = subprocess.run(cmd, cwd=ROOT, stdout=subprocess.PIPE,
                             stderr=subprocess.PIPE, timeout=300)
    except (OSError, subprocess.SubprocessError) as e:
        sys.exit("FAIL  跑不动 %s：%s —— 白名单抽取需要 Go 工具链"
                 % (" ".join(cmd), e))
    if out.returncode != 0:
        sys.stderr.write(out.stderr.decode("utf-8", "replace"))
        sys.exit("FAIL  %s 失败（退出码 %d）" % (" ".join(cmd), out.returncode))
    try:
        return json.loads(out.stdout.decode("utf-8"))
    except ValueError as e:
        sys.exit("FAIL  %s 的输出不是合法 JSON：%s" % (" ".join(cmd), e))


def golden(dump):
    """实抽结果 + 手工登记表 → golden 结构。"""
    def split(items):
        """分两档：all=True 的进"通用"，其余按标签归拢。"""
        universal, by_tag = [], {}
        for e in items:
            name = e["name"]
            if name in EVENT_EXEMPT:
                continue
            if e["all"]:
                universal.append(name)
                continue
            for t in sorted(e["tags"]):
                by_tag.setdefault(t, []).append(name)
        return sorted(universal), {t: sorted(v) for t, v in sorted(by_tag.items())}

    pu, pt = split(dump["props"])
    eu, et = split(dump["events"])

    return {
        "说明": "本文件由 python3 scripts/gen-props-golden.py 生成，是「gfx 内核认得哪些 "
                "属性/事件」的唯一机器可读真相。真源是 gfx/*.go 的**读取点**（写入点不算），"
                "由 tools/propdump（go/ast + 调用图）抽出后按标签归拢。改了 gfx 的读取点后"
                "请重新生成并提交 —— CI 会用「实抽 vs 本文件」diff 兜住漏改。",
        "生成方式": "python3 scripts/gen-props-golden.py",
        "真源": "gfx/*.go（PropNum/PropStr/PropBool/PropHas/PropHandler/Props[\"…\"] 的读取点 "
                "+ callHandler/handlerInChain 的事件派发点），经 tools/propdump 抽取",
        "tagCount": len(dump["tags"]),
        "tags": sorted(dump["tags"]),
        # 所有标签都认 —— 抽取时判不出标签归属的一律落这里（过宽只漏报，过窄才误报）
        "universalProps": pu,
        "propsByTag": pt,
        "universalEvents": eu,
        "eventsByTag": et,
        # 手工登记：内核没有派发点但对外承诺过的事件。报错文案必须明说"还没实现"。
        "eventsNotImplemented": {k: EVENTS_NOT_IMPLEMENTED[k]
                                 for k in sorted(EVENTS_NOT_IMPLEMENTED)},
        # 手工登记：有明确替代写法的常见误解（键是 "标签|属性"，"*" = 任意标签）
        "propHints": {k: PROP_HINTS[k] for k in sorted(PROP_HINTS)},
        # 框架键：在 h() 里被指令层先消费，不进节点 Props 的读取路径
        "frameworkProps": {k: FRAMEWORK_PROPS[k] for k in sorted(FRAMEWORK_PROPS)},
        "prefixExempt": {k: PROP_PREFIX_EXEMPT[k] for k in sorted(PROP_PREFIX_EXEMPT)},
        # 事件名豁免：不是事件回调（走 effect 求值），不该按事件校验
        "eventExempt": {k: EVENT_EXEMPT[k] for k in sorted(EVENT_EXEMPT)},
    }


def go_source(data):
    """渲染 gfx/knownprops_gen.go。与 golden 同一批数据，不做二次加工。"""
    def maplit(names, indent):
        pad = "\t" * indent
        if not names:
            return "{}"
        lines = ["{"]
        for n in names:
            lines.append('%s\t%s: {},' % (pad, json.dumps(n, ensure_ascii=False)))
        lines.append(pad + "}")
        return "\n".join(lines)

    def strmap(d, indent=0):
        pad = "\t" * indent
        if not d:
            return "{}"
        lines = ["{"]
        for k in sorted(d):
            lines.append("%s\t%s: %s," % (pad, json.dumps(k, ensure_ascii=False),
                                          json.dumps(d[k], ensure_ascii=False)))
        lines.append(pad + "}")
        return "\n".join(lines)

    out = []
    out.append("// Code generated by scripts/gen-props-golden.py. DO NOT EDIT.")
    out.append("//")
    out.append("// 真源是 gfx/*.go 的属性**读取点**（写入点不算），由 tools/propdump")
    out.append("// 用 go/ast + 调用图抽出后按标签归拢；机器可读的同一份数据在")
    out.append("// docs/props.golden.json。改了 gfx 的读取点后跑")
    out.append("// python3 scripts/gen-props-golden.py 重新生成 —— CI 有 diff 闸门。")
    out.append("package gfx")
    out.append("")
    out.append("// knownPropsUniversal 是所有标签都读的属性。")
    out.append("//")
    out.append("// 「判不出标签归属就落这里」是刻意的：过宽只是漏报（少警告一次），过窄")
    out.append("// 才是误报（警告一个真在用的属性），而误报会让人直接把 strictAPI 关掉。")
    out.append("var knownPropsUniversal = map[string]struct{}{")
    for n in data["universalProps"]:
        out.append("\t%s: {}," % json.dumps(n, ensure_ascii=False))
    out.append("}")
    out.append("")
    out.append("// knownPropsByTag 是各标签**额外**认得的属性（不含 universal 那一档）。")
    out.append("var knownPropsByTag = map[string]map[string]struct{}{")
    for tag in sorted(data["propsByTag"]):
        out.append("\t%s: %s," % (json.dumps(tag, ensure_ascii=False),
                                  maplit(data["propsByTag"][tag], 1)))
    out.append("}")
    out.append("")
    out.append("// knownEventsUniversal 是所有标签都能收到的事件（内核有派发点）。")
    out.append("var knownEventsUniversal = map[string]struct{}{")
    for n in data["universalEvents"]:
        out.append("\t%s: {}," % json.dumps(n, ensure_ascii=False))
    out.append("}")
    out.append("")
    out.append("// knownEventsByTag 是各标签**额外**认得的事件。")
    out.append("var knownEventsByTag = map[string]map[string]struct{}{")
    for tag in sorted(data["eventsByTag"]):
        out.append("\t%s: %s," % (json.dumps(tag, ensure_ascii=False),
                                  maplit(data["eventsByTag"][tag], 1)))
    out.append("}")
    out.append("")
    out.append("// eventsNotImplemented 是**对外承诺过但内核还没有派发点**的事件。")
    out.append("//")
    out.append("// 这一档必须单独报：把它们混进「未知属性」会让人去查自己脚本的拼写，")
    out.append("// 查一天也查不出来 —— 问题是内核还没写。值是给用户的说明。")
    out.append("var eventsNotImplemented = map[string]string%s" % strmap(data["eventsNotImplemented"]))
    out.append("")
    out.append("// propHints 是**有明确替代写法**的常见误解（键 \"标签|属性\"，\"*\" = 任意标签）。")
    out.append("//")
    out.append("// 与 eventsNotImplemented 同理：通用文案「未知属性」帮不上忙，用户要的是")
    out.append("// 「那我该怎么写」。每条都来自实扫证据，新增要带理由。")
    out.append("var propHints = map[string]string%s" % strmap(data["propHints"]))
    out.append("")
    out.append("// frameworkProps 是在 h() 里被指令层先消费的键（each/show/model/…）。")
    out.append("// 它们不进节点 Props 的读取路径，写它们是**正确用法**，不能报未知属性。")
    out.append("var frameworkProps = map[string]string%s" % strmap(data["frameworkProps"]))
    out.append("")
    out.append("// eventExempt 是以 on 开头但**不是事件回调**的键（走 effect 求值的响应式")
    out.append("// 函数 prop）。isEventPropName 只判前缀，认不出这一层。")
    out.append("var eventExempt = map[string]string%s" % strmap(data["eventExempt"]))
    out.append("")
    out.append("// propPrefixExempt 是不进本白名单、**由别的层**校验或本就不该校验的名字前缀。")
    out.append("var propPrefixExempt = []string{")
    for k in sorted(data["prefixExempt"]):
        out.append("\t%s," % json.dumps(k, ensure_ascii=False))
    out.append("}")
    out.append("")
    return "\n".join(out)


def gofmt_text(text):
    """过一遍 gofmt，保证生成的文件与仓库其余 Go 源码同一副样子。

    不过的话 `gofmt -l` 会把它列出来（映射字面量的键要对齐），而"生成的文件
    天生不合规"会让 gofmt 检查慢慢失去意义。
    """
    try:
        out = subprocess.run(["gofmt"], input=text.encode("utf-8"),
                             stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                             timeout=60)
    except (OSError, subprocess.SubprocessError):
        return text
    if out.returncode != 0:
        sys.stderr.write(out.stderr.decode("utf-8", "replace"))
        return text
    return out.stdout.decode("utf-8")


def diff(golden_data, actual):
    problems = []
    for field in ("tags", "universalProps", "universalEvents"):
        g, a = golden_data.get(field, []), actual.get(field, [])
        if sorted(g) != sorted(a):
            gs, as_ = set(g), set(a)
            problems.append("  - %s：新增 %s；消失 %s"
                            % (field, " ".join(sorted(as_ - gs)) or "（无）",
                               " ".join(sorted(gs - as_)) or "（无）"))
    for field in ("propsByTag", "eventsByTag"):
        g, a = golden_data.get(field, {}), actual.get(field, {})
        for tag in sorted(set(g) | set(a)):
            gv, av = set(g.get(tag, [])), set(a.get(tag, []))
            if gv != av:
                problems.append("  - %s[%s]：新增 %s；消失 %s"
                                % (field, tag, " ".join(sorted(av - gv)) or "（无）",
                                   " ".join(sorted(gv - av)) or "（无）"))
    for field in ("eventsNotImplemented", "propHints", "frameworkProps",
                  "prefixExempt", "eventExempt"):
        g, a = golden_data.get(field, {}), actual.get(field, {})
        if g != a:
            for k in sorted(set(g) | set(a)):
                if g.get(k) != a.get(k):
                    problems.append("  - %s[%s]：golden=%r 实抽=%r"
                                    % (field, k, g.get(k), a.get(k)))
    return problems


def main():
    argv = sys.argv[1:]
    actual = golden(run_propdump())
    # 抽取器自身的守卫: gfx 的读取点不可能一个都没有。抽到 0 个说明抽取逻辑
    # 已经失效 —— 这时候"golden 与代码一致"是句废话（两边一起变空），必须红。
    if not actual["universalProps"] and not actual["propsByTag"]:
        sys.exit("FAIL  实抽到 0 个属性 —— tools/propdump 多半已经失效"
                 "（gfx 的读取点不可能一个都没有），先修抽取器再谈 golden")
    text = json.dumps(actual, ensure_ascii=False, indent=2, sort_keys=False) + "\n"
    go_text = gofmt_text(go_source(actual))

    if "--print" in argv:
        sys.stdout.write(text)
        return 0

    golden_path = os.path.join(ROOT, GOLDEN_REL)
    go_path = os.path.join(ROOT, GOFILE_REL)

    if "--check" in argv:
        problems = []
        if not os.path.isfile(golden_path):
            print("FAIL  属性白名单 golden：%s 不存在 —— 先跑 "
                  "python3 scripts/gen-props-golden.py 生成它" % GOLDEN_REL)
            return 1
        with open(golden_path, encoding="utf-8") as f:
            try:
                g = json.load(f)
            except ValueError as e:
                print("FAIL  属性白名单 golden：%s 不是合法 JSON：%s" % (GOLDEN_REL, e))
                return 1
        problems += diff(g, actual)
        if not os.path.isfile(go_path):
            problems.append("  - %s 不存在（应随 golden 一起生成）" % GOFILE_REL)
        else:
            with open(go_path, encoding="utf-8") as f:
                if f.read() != go_text:
                    problems.append("  - %s 与 golden 不同步（重新生成并提交）"
                                    % GOFILE_REL)
        if problems:
            print("FAIL  属性白名单 golden 与代码不一致 —— gfx 的读取点变了要让人看见："
                  "确认无误后跑 python3 scripts/gen-props-golden.py 重新生成并随代码一起提交")
            print("\n".join(problems))
            return 1
        print("ok    属性白名单 golden 与代码一致（%d 个标签，通用属性 %d 个，"
              "按标签 %d 组；通用事件 %d 个）"
              % (actual["tagCount"], len(actual["universalProps"]),
                 len(actual["propsByTag"]), len(actual["universalEvents"])))
        return 0

    with open(golden_path, "w", encoding="utf-8") as f:
        f.write(text)
    with open(go_path, "w", encoding="utf-8") as f:
        f.write(go_text)
    print("已写入 %s 与 %s（%d 个标签；通用属性 %d / 按标签 %d 组；"
          "通用事件 %d / 按标签 %d 组；未实现事件 %d）"
          % (GOLDEN_REL, GOFILE_REL, actual["tagCount"],
             len(actual["universalProps"]), len(actual["propsByTag"]),
             len(actual["universalEvents"]), len(actual["eventsByTag"]),
             len(actual["eventsNotImplemented"])))
    return 0


if __name__ == "__main__":
    sys.exit(main())
