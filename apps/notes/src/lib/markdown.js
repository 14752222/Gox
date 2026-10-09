// Markdown 解析与渲染 —— 纯逻辑层（不 import gox，可脱离 UI 单独跑）。
//
// ## 输出为什么是"节点描述树"而不是直接 h(...)
//
// `apps/README.md` 第 4 条：**`lib/` 不许 import `gox`** —— 否则"脱离 UI 单测"
// 这条路就断了（探针脚本里没有 gfx）。所以本文件产出的是**节点描述树**：
//
//	{ tag: "column", props: { gap: 10 }, children: [ { tag: "text", … }, "字符串" ] }
//
// 形状与 `h(tag, props, ...children)` 的入参一一对应，由 `components/` 层用
// 真正的 `h()` 物化成 GuiNode。换句话说是"延后一层的 JSX" —— 描述树里写的
// 都是 gfx 认识的标签（column / row / text / rect / table …），只是不负责
// 组装而已。
//
// ## 不做什么（明确边界）
//
// 这是一个**够用就好**的子集，不是 CommonMark 实现。刻意不做的：
//
//   - HTML 块 / 行内 HTML（Gox 的 GUI 里没有 HTML）
//   - 引用块、列表项的**任意嵌套**（引用支持多段；列表支持一层缩进嵌套，
//     但嵌套块里不再解析子块）
//   - 脚注、任务列表 `- [ ]`、定义列表、自动链接 `<http://…>`
//   - 转义只认 `\` + 标点；`&amp;` 实体不还原
//
// 需要更多语法时，扩展点是 `parseBlock` 的表与 `parseInline` 的分支 —— 两处
// 都是"加一个分支"，不会牵动渲染层。
//
// ## ⚠️ 引擎限制：别用 `<` `>` 比较字符串
//
// Gox 当前的字符串**关系比较恒为 false**（`"a" < "b"` → `false`）。本文件所有
// 字符判定一律走 `charCodeAt` / `indexOf`，不写 `c < 'z'` 这类比较。改这段
// 代码时别"顺手简化"成关系比较 —— 会静默失配且不报错。

import { estimateLines } from "./text-metrics.js";

// ===== 字符小工具 =====
//
// 判据一律用 charCodeAt（见文件头的引擎限制说明）。

export function isDigitChar(c) {
  return c !== "" && "0123456789".indexOf(c) >= 0;
}

export function isSpaceChar(c) {
  return c === " " || c === "\t";
}

export function isFenceChar(c) {
  return c === "`" || c === "~";
}

export function num(v, def) {
  return typeof v === "number" && !isNaN(v) ? v : def;
}

export function str(v, def) {
  return v === undefined || v === null ? def : String(v);
}

export function trim(s) {
  return str(s, "").trim();
}

export function mkRun(text, bold, italic, code, href) {
  return { text: text, bold: bold, italic: italic, code: code, href: href };
}

// ===== 行内解析 =====
//
// run 的形状: { text, bold, italic, code, href }
// 一个 run 是一段**样式一致**的文本；相邻不同样式的 run 在渲染层拼成一行。

export function parseInline(text) {
  const src = str(text, "");
  const runs = [];
  const st = { i: 0, buf: "" };

  // flush: 把攒在 buf 里的普通文本落成一个 run
  const flush = () => {
    if (st.buf !== "") {
      runs.push(mkRun(st.buf, false, false, false, ""));
      st.buf = "";
    }
  };
  const push = (t, bold, italic, code, href) => {
    flush();
    runs.push(mkRun(t, bold, italic, code, href));
  };

  while (st.i < src.length) {
    const c = src.charAt(st.i);

    // 转义: \x → 字面 x（且 x 不再触发后面的强调判定）
    if (c === "\\" && st.i + 1 < src.length) {
      st.buf = st.buf + src.charAt(st.i + 1);
      st.i = st.i + 2;
      continue;
    }

    // 行内代码: `code`
    if (c === "`") {
      const end = src.indexOf("`", st.i + 1);
      if (end > st.i) {
        push(src.slice(st.i + 1, end), false, false, true, "");
        st.i = end + 1;
        continue;
      }
    }

    // 加粗: **bold**（必须排在单 * 之前，否则 `**` 会被当成两个 `*x*`）
    if (c === "*" && src.charAt(st.i + 1) === "*") {
      const end = src.indexOf("**", st.i + 2);
      if (end > st.i + 1) {
        push(src.slice(st.i + 2, end), true, false, false, "");
        st.i = end + 2;
        continue;
      }
    }

    // 斜体: *it* / _it_
    if (c === "*" || c === "_") {
      const end = src.indexOf(c, st.i + 1);
      if (end > st.i) {
        push(src.slice(st.i + 1, end), false, true, false, "");
        st.i = end + 1;
        continue;
      }
    }

    // 链接: [text](href)
    if (c === "[") {
      const close = src.indexOf("]", st.i);
      if (close > st.i && src.charAt(close + 1) === "(") {
        const paren = src.indexOf(")", close + 2);
        if (paren > close) {
          push(src.slice(st.i + 1, close), false, false, false, src.slice(close + 2, paren));
          st.i = paren + 1;
          continue;
        }
      }
    }

    st.buf = st.buf + c;
    st.i = st.i + 1;
  }
  flush();
  return runs;
}


// inlineText 把 runs 拼回纯文本（渲染成表格单元格等"只要字"的场合用）。
export function inlineText(runs) {
  let s = "";
  for (let i = 0; i < runs.length; i++) s = s + runs[i].text;
  return s;
}

// ===== 块级解析 =====

// splitLines: 按行切分，同时吃掉 \r（Windows 的 \r\n 与老 Mac 的裸 \r 都收）。
export function splitLines(src) {
  const s = str(src, "");
  const out = [];
  let cur = "";
  for (let i = 0; i < s.length; i++) {
    const c = s.charAt(i);
    if (c === "\n") {
      out.push(cur);
      cur = "";
      continue;
    }
    if (c === "\r") {
      out.push(cur);
      cur = "";
      if (s.charAt(i + 1) === "\n") i = i + 1;
      continue;
    }
    cur = cur + c;
  }
  out.push(cur);
  return out;
}

export function isBlank(line) {
  return trim(line) === "";
}

// indentOf: 前导空格数（只数空格，不数 tab —— tab 当 0，避免与代码块混淆）。
export function indentOf(line) {
  let i = 0;
  while (i < line.length && line.charAt(i) === " ") i = i + 1;
  return i;
}

export function isFence(line) {
  const t = trim(line);
  if (t.length < 3) return false;
  const c = t.charAt(0);
  if (!isFenceChar(c)) return false;
  return t.charAt(1) === c && t.charAt(2) === c;
}

// fenceLang: ```js → "js"。起始围栏行后面那串就是语言标注。
export function fenceLang(line) {
  return trim(trim(line).slice(3));
}

export function isHr(line) {
  const t = trim(line);
  if (t.length < 3) return false;
  const c = t.charAt(0);
  if (c !== "-" && c !== "*" && c !== "_") return false;
  let n = 0;
  for (let i = 0; i < t.length; i++) {
    const ch = t.charAt(i);
    if (ch === c) n = n + 1;
    else if (!isSpaceChar(ch)) return false;
  }
  return n >= 3;
}

// headingLevel: ATX 标题 `#`~`######`，**# 后必须有空白**。
//
// 为什么要求空白：`#标签` 在笔记里是话题标签的写法，不当标题 —— 宽松判定的话
// 一行话题标签会被莫名其妙放大成标题。
export function headingLevel(line) {
  let n = 0;
  while (n < 6 && n < line.length && line.charAt(n) === "#") n = n + 1;
  if (n === 0) return 0;
  const after = line.charAt(n);
  if (!isSpaceChar(after)) return 0;
  return n;
}

// quotePrefix: `>` 引用的缩进（无 → -1）。缩进超过 3 空格不当引用，
// 与 CommonMark 同口径 —— 那已经接近代码块的语义了。
export function quotePrefix(line) {
  const ind = indentOf(line);
  if (ind > 3) return -1;
  if (line.charAt(ind) === ">") return ind;
  return -1;
}

// listItemInfo: 解析一行列表项 → { indent, ordered, text }，不是列表项返回 null。
//
// 收两种标记：无序 `- * +`，有序 `1. 1)`（数字最长 9 位，防误吞长串数字）。
// 标记后必须跟空白 —— 否则 `-5℃` 会被当成列表。
export function listItemInfo(line) {
  const ind = indentOf(line);
  if (ind > 8) return null; // 缩进过深：多为代码块内容，不当列表
  const rest = line.slice(ind);
  if (rest.length < 2) return null;
  const c0 = rest.charAt(0);
  const c1 = rest.charAt(1);

  if (c0 === "-" || c0 === "*" || c0 === "+") {
    if (!isSpaceChar(c1)) return null;
    return { indent: ind, ordered: false, text: rest.slice(2) };
  }

  if (isDigitChar(c0)) {
    let j = 1;
    while (j < rest.length && j < 10 && isDigitChar(rest.charAt(j))) j = j + 1;
    const sep = rest.charAt(j);
    if (sep !== "." && sep !== ")") return null;
    if (!isSpaceChar(rest.charAt(j + 1))) return null;
    return { indent: ind, ordered: true, text: rest.slice(j + 2) };
  }

  return null;
}

// parseFence: 收走 ``` 之间的原样行（不解析行内语法 —— 代码就是代码）。
export function parseFence(lines, st) {
  const lang = fenceLang(lines[st.i]);
  const marker = trim(lines[st.i]).charAt(0);
  st.i = st.i + 1;
  const body = [];
  let closed = false;
  while (st.i < lines.length) {
    const t = trim(lines[st.i]);
    if (t.length >= 3 && t.charAt(0) === marker && t.charAt(1) === marker && t.charAt(2) === marker) {
      st.i = st.i + 1;
      closed = true;
      break;
    }
    body.push(lines[st.i]);
    st.i = st.i + 1;
  }
  return { kind: "code", lang: lang, lines: body, closed: closed };
}

// parseQuote: 收走连续的 `>` 行，空 `>` 行分段。
export function parseQuote(lines, st) {
  const paras = [];
  let cur = "";
  while (st.i < lines.length) {
    const line = lines[st.i];
    const ind = quotePrefix(line);
    if (ind < 0) break;
    let body = line.slice(ind + 1);
    if (body.charAt(0) === " ") body = body.slice(1);
    body = trim(body);
    if (body === "") {
      if (cur !== "") {
        paras.push(cur);
        cur = "";
      }
    } else {
      cur = cur === "" ? body : cur + " " + body;
    }
    st.i = st.i + 1;
  }
  if (cur !== "") paras.push(cur);
  const paragraphs = [];
  for (let i = 0; i < paras.length; i++) paragraphs.push(parseInline(paras[i]));
  return { kind: "quote", paragraphs: paragraphs };
}

// parseList: 收走连续的列表项。缩进按 2 空格一级折算成 depth（渲染层缩进用）。
//
// 刻意不做：列表项里的子块（子列表 / 代码块）。那需要"块级递归 + 缩进回溯"，
// 是一个量级更大的活；笔记场景里一层缩进够用。
export function parseList(lines, st) {
  const first = listItemInfo(lines[st.i]);
  const ordered = first.ordered;
  const items = [];
  while (st.i < lines.length) {
    const line = lines[st.i];
    if (isBlank(line)) {
      // 允许项之间空一行（松散列表）；空行后若不再是列表项就收工
      if (st.i + 1 < lines.length && listItemInfo(lines[st.i + 1]) !== null) {
        st.i = st.i + 1;
        continue;
      }
      break;
    }
    const info = listItemInfo(line);
    if (info === null) {
      // 缩进的续行：接到上一项文本后面（软换行）
      const ind = indentOf(line);
      if (items.length > 0 && ind >= first.indent + 1) {
        items[items.length - 1].text = items[items.length - 1].text + " " + trim(line);
        st.i = st.i + 1;
        continue;
      }
      break;
    }
    items.push({
      text: info.text,
      ordered: info.ordered,
      indent: info.indent,
    });
    st.i = st.i + 1;
  }
  const out = [];
  for (let i = 0; i < items.length; i++) {
    out.push({
      runs: parseInline(items[i].text),
      ordered: items[i].ordered,
      indent: items[i].indent,
    });
  }
  return { kind: "list", ordered: ordered, items: out };
}

// splitRow: 表格的一行 → 单元格数组（吃掉首尾的 `|`）。
export function splitRow(line) {
  let s = trim(line);
  if (s.charAt(0) === "|") s = s.slice(1);
  if (s.length > 0 && s.charAt(s.length - 1) === "|") s = s.slice(0, s.length - 1);
  return s.split("|");
}

// isTableStart: 表头行的下一行是 `---|:---:|---:` 这样的分隔行才算表格。
//
// 为什么要求分隔行：只看到 `|` 就把行当表格的话，正文里出现一个竖线
// （比如 `a|b` 这种代码注释）就会被误判成表格。
export function isTableStart(lines, i) {
  if (i + 1 >= lines.length) return false;
  const head = trim(lines[i]);
  const delim = trim(lines[i + 1]);
  if (head.indexOf("|") < 0 || delim.indexOf("|") < 0) return false;
  const cells = splitRow(delim);
  if (cells.length === 0) return false;
  for (let k = 0; k < cells.length; k++) {
    const c = trim(cells[k]);
    if (c.length === 0) return false;
    for (let j = 0; j < c.length; j++) {
      const ch = c.charAt(j);
      if (ch !== "-" && ch !== ":") return false;
    }
  }
  return true;
}

// alignmentOf: 分隔单元格 → left / center / right。
export function alignmentOf(cell) {
  const c = trim(cell);
  const l = c.charAt(0) === ":";
  const r = c.charAt(c.length - 1) === ":";
  if (l && r) return "center";
  if (r) return "right";
  return "left";
}

export function parseTable(lines, st) {
  const header = [];
  const headCells = splitRow(lines[st.i]);
  for (let i = 0; i < headCells.length; i++) header.push(trim(headCells[i]));

  const align = [];
  const delimCells = splitRow(lines[st.i + 1]);
  for (let i = 0; i < delimCells.length; i++) align.push(alignmentOf(delimCells[i]));

  st.i = st.i + 2;
  const rows = [];
  while (st.i < lines.length) {
    const line = lines[st.i];
    if (isBlank(line) || line.indexOf("|") < 0) break;
    const cells = splitRow(line);
    const row = [];
    for (let i = 0; i < cells.length; i++) row.push(trim(cells[i]));
    rows.push(row);
    st.i = st.i + 1;
  }
  return { kind: "table", header: header, align: align, rows: rows };
}

// parseParagraph: 收走连续非空行，拼成一段（软换行按空格合并）。
export function parseParagraph(lines, st) {
  let text = "";
  while (st.i < lines.length) {
    const line = lines[st.i];
    if (isBlank(line)) break;
    if (isFence(line) || isHr(line) || headingLevel(line) > 0) break;
    if (quotePrefix(line) >= 0) break;
    if (listItemInfo(line) !== null) break;
    text = text === "" ? trim(line) : text + " " + trim(line);
    st.i = st.i + 1;
  }
  return { kind: "paragraph", runs: parseInline(text) };
}

// parseMarkdown: 行 → 块。判据顺序是有讲究的（围栏 > 分隔线 > 标题 >
// 引用 > 列表 > 段落）：越靠前的形态越"整行专属"。
export function parseMarkdown(src) {
  const lines = splitLines(src);
  const blocks = [];
  const st = { i: 0 };
  while (st.i < lines.length) {
    if (isBlank(lines[st.i])) {
      st.i = st.i + 1;
      continue;
    }
    if (isFence(lines[st.i])) {
      blocks.push(parseFence(lines, st));
      continue;
    }
    if (isHr(lines[st.i])) {
      blocks.push({ kind: "hr" });
      st.i = st.i + 1;
      continue;
    }
    const h = headingLevel(lines[st.i]);
    if (h > 0) {
      blocks.push({ kind: "heading", level: h, runs: parseInline(trim(lines[st.i].slice(h))) });
      st.i = st.i + 1;
      continue;
    }
    if (quotePrefix(lines[st.i]) >= 0) {
      blocks.push(parseQuote(lines, st));
      continue;
    }
    if (isTableStart(lines, st.i)) {
      blocks.push(parseTable(lines, st));
      continue;
    }
    if (listItemInfo(lines[st.i]) !== null) {
      blocks.push(parseList(lines, st));
      continue;
    }
    blocks.push(parseParagraph(lines, st));
  }
  return blocks;
}

// ===== 渲染成 gfx 节点描述树 =====

// el / tx 是描述树的两个构造器：元素与纯文本。
export function el(tag, props, children) {
  return { tag: tag, props: props, children: children };
}

export function tx(text) {
  return el("#text", null, [text]);
}

// normalizeStyle: 把外部给的排版表补成完整的（缺省值集中在 theme.js 里给出，
// 这里再兜一层是为了让 renderMarkdown 不传 style 也能跑 —— 探针脚本就靠这条）。
export function normalizeStyle(style) {
  const s = style || {};
  const font = num(s.font, 13);
  return {
    width: num(s.width, 520),
    font: font,
    lineHeight: num(s.lineHeight, Math.round(font * 1.6)),
    color: str(s.color, "#1c2430"),
    headFont: s.headFont || [font + 8, font + 4, font + 2, font, font, font - 1],
    headColor: str(s.headColor, "#141b26"),
    codeFont: num(s.codeFont, font - 2),
    codeColor: str(s.codeColor, "#a13d5c"),
    codeBg: str(s.codeBg, "#eef1f6"),
    codeFamily: str(s.codeFamily, "monospace"),
    quoteColor: str(s.quoteColor, "#55607a"),
    quoteBar: str(s.quoteBar, "#c9d2e0"),
    linkColor: str(s.linkColor, "#1a5fb4"),
    hrColor: str(s.hrColor, "#c3cad6"),
    listGap: num(s.listGap, 2),
    blockGap: num(s.blockGap, 10),
  };
}

// runProps: 一个行内片段 → <text> 的 props。
//
// 粗/斜用 fontWeight / fontStyle 表达，等宽用 fontFamily —— gfx 的这两根轴
// **沿父链继承**，于是行内片段只要写在自己那一段上就够，不用给整段兜底。
export function runProps(run, style) {
  const p = {
    font: style.font,
    color: style.color,
    lineHeight: style.lineHeight,
    fontWeight: "normal",
    fontStyle: "normal",
  };
  if (run.code) {
    p.fontFamily = style.codeFamily;
    p.color = style.codeColor;
    p.background = style.codeBg;
    return p;
  }
  if (run.bold) p.fontWeight = "bold";
  if (run.italic) p.fontStyle = "italic";
  if (run.href !== "") p.color = style.linkColor;
  return p;
}

// splitWords: 把一段文本切成"可折行的词"。
//
// 为什么要切：gfx 的一段 <text> 是**一个盒子**，折行只能发生在盒子之间。
// 于是要让中英混排的段落自动换行，就得让每个可断点成为独立盒子：
//   - 拉丁词按空格切（空格留在词尾，于是词距不用靠 gap 补）；
//   - CJK 逐字切 —— 中文没有空格，逐字断行本来就是它的正确行为。
export function splitWords(s) {
  const out = [];
  let cur = "";
  for (let i = 0; i < s.length; i++) {
    const ch = s.charAt(i);
    if (ch === " ") {
      if (cur !== "") {
        out.push(cur + " ");
        cur = "";
      } else {
        out.push(" ");
      }
      continue;
    }
    if (s.charCodeAt(i) > 0x2e80) {
      if (cur !== "") {
        out.push(cur);
        cur = "";
      }
      out.push(ch);
      continue;
    }
    cur = cur + ch;
  }
  if (cur !== "") out.push(cur);
  return out;
}

// renderRuns: 一段行内片段 → <row wrap> 描述。
export function renderRuns(runs, style) {
  const kids = [];
  for (let i = 0; i < runs.length; i++) {
    const run = runs[i];
    if (run.text === "") continue;
    const words = splitWords(run.text);
    const props = runProps(run, style);
    for (let k = 0; k < words.length; k++) {
      kids.push(el("text", props, [words[k]]));
    }
  }
  if (kids.length === 0) kids.push(tx(""));
  return el("row", { wrap: true, gap: 0, width: style.width, alignItems: "start" }, kids);
}

// quoteBarHeight 估算引用块左侧竖条的高度。
//
// 为什么是估算：脚本侧量不到 <text> 的真实高度（见 text-metrics.js 的说明），
// 只能按"这段字大概折几行 × 行高"算。竖条**宁长勿短** —— 短一截会和文字
// 一起断掉很难看，长一截只是多出一点空白，所以末尾再给一行余量。
// 可用宽与 renderBlock 里正文列同口径（W - 20）。
export function quoteBarHeight(block, style) {
  const ps = block.paragraphs;
  let lines = 0;
  for (let i = 0; i < ps.length; i++) {
    lines = lines + estimateLines(inlineText(ps[i]), style.font, style.width - 20);
  }
  if (lines < 1) lines = 1;
  const gap = ps.length > 1 ? (ps.length - 1) * 4 : 0;
  return (lines + 1) * style.lineHeight + gap;
}

// renderBlock: 一个块 → 描述节点。
export function renderBlock(block, style) {
  const W = style.width;

  if (block.kind === "hr") {
    return el("rect", { width: W, height: 1, background: style.hrColor }, []);
  }

  if (block.kind === "heading") {
    let font = style.font;
    if (block.level >= 1 && block.level <= 6) font = style.headFont[block.level - 1];
    const kids = [];
    const runs = block.runs;
    for (let i = 0; i < runs.length; i++) {
      const p = runProps(runs[i], style);
      p.font = font;
      p.color = style.headColor;
      kids.push(el("text", p, [runs[i].text]));
    }
    if (kids.length === 0) kids.push(tx(""));
    return el("row", { wrap: true, gap: 0, width: W, alignItems: "start" }, kids);
  }

  if (block.kind === "code") {
    // 代码块 = 一块带底的 column + 一行一个 <text>（等宽）。
    //
    // 刻意**不开 wrap**：代码折行会让缩进失效，读起来更糟；超长行宁可截断。
    // 空行补一个空格 —— 否则那个 <text> 高度为 0，整行被吃掉。
    const kids = [];
    const lines = block.lines;
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i] === "" ? " " : lines[i];
      kids.push(el("text", {
        font: style.codeFont,
        color: style.codeColor,
        fontFamily: style.codeFamily,
        lineHeight: style.codeFont + 4,
      }, [line]));
    }
    return el("column", {
      gap: 0,
      background: style.codeBg,
      padding: 8,
      width: W,
    }, kids);
  }

  if (block.kind === "quote") {
    const paras = [];
    const ps = block.paragraphs;
    for (let i = 0; i < ps.length; i++) paras.push(renderRuns(ps[i], style));
    // 竖条用一块窄 rect：高度按估算行数算（见 estimateLines 的说明）。
    const barH = quoteBarHeight(block, style);
    return el("row", { gap: 8, width: W, alignItems: "start" }, [
      el("rect", { width: 3, height: barH, background: style.quoteBar }, []),
      el("column", { gap: 4, width: W - 20, font: style.font, color: style.quoteColor }, paras),
    ]);
  }

  if (block.kind === "list") {
    const kids = [];
    const items = block.items;
    for (let i = 0; i < items.length; i++) {
      const it = items[i];
      const marker = it.ordered ? (i + 1) + "." : "•";
      const depth = Math.floor(it.indent / 2);
      kids.push(el("row", {
        gap: 6,
        width: W,
        alignItems: "start",
        paddingLeft: depth * 16,
      }, [
        el("text", { font: style.font, color: style.color }, [marker]),
        el("column", { gap: 0, width: W - 28 }, [renderRuns(it.runs, style)]),
      ]));
    }
    return el("column", { gap: style.listGap, width: W }, kids);
  }

  if (block.kind === "table") {
    const cols = [];
    for (let c = 0; c < block.header.length; c++) {
      cols.push({
        key: "c" + c,
        label: block.header[c],
        align: c < block.align.length ? block.align[c] : "left",
      });
    }
    const rows = [];
    for (let r = 0; r < block.rows.length; r++) {
      const src = block.rows[r];
      const obj = {};
      for (let c = 0; c < cols.length; c++) {
        obj[cols[c].key] = c < src.length ? src[c] : "";
      }
      rows.push(obj);
    }
    return el("table", { columns: cols, rows: rows, zebra: true, width: W }, []);
  }

  // paragraph
  return renderRuns(block.runs, style);
}

// renderMarkdown: Markdown 文本 → gfx 节点描述树。
//
// 返回的根节点是一个 <column>；`components/markdown-view.js` 用 h() 把它物化。
// 库本身不碰 h —— 于是这份输出可以在探针脚本里被直接断言结构。
export function renderMarkdown(src, style) {
  const st2 = normalizeStyle(style);
  const blocks = parseMarkdown(src);
  const kids = [];
  for (let i = 0; i < blocks.length; i++) kids.push(renderBlock(blocks[i], st2));
  return el("column", { gap: st2.blockGap, width: st2.width }, kids);
}

// ===== 取纯文本（给搜索 / 摘要 / 测试用）=====

// blockText: 一个块的纯文本。
export function blockText(block) {
  if (block.kind === "code") return block.lines.join("\n");
  if (block.kind === "quote") {
    const ps = [];
    for (let i = 0; i < block.paragraphs.length; i++) ps.push(inlineText(block.paragraphs[i]));
    return ps.join("\n");
  }
  if (block.kind === "list") {
    const its = [];
    for (let i = 0; i < block.items.length; i++) its.push(inlineText(block.items[i].runs));
    return its.join("\n");
  }
  if (block.kind === "table") {
    const rs = [block.header.join(" | ")];
    for (let i = 0; i < block.rows.length; i++) rs.push(block.rows[i].join(" | "));
    return rs.join("\n");
  }
  if (block.kind === "hr") return "---";
  return inlineText(block.runs);
}

// markdownText: 整篇的纯文本（块之间空行分隔）。
export function markdownText(src) {
  const blocks = parseMarkdown(src);
  const out = [];
  for (let i = 0; i < blocks.length; i++) out.push(blockText(blocks[i]));
  return out.join("\n\n");
}

// nodeText: 描述树 → 纯文本（递归拼所有字符串子节点）。
export function nodeText(node) {
  if (node === null || node === undefined) return "";
  if (typeof node === "string") return node;
  const kids = node.children || [];
  let s = "";
  for (let i = 0; i < kids.length; i++) s = s + nodeText(kids[i]);
  return s;
}

// ===== 为什么连内部辅助函数都写着 `export function`，且顺序不能随便调 =====
//
// 与 chart.js 同一条理由，是**两条引擎限制**逼出来的（最小复现见 apps/notes/README.md
// 「引擎限制」一节）：
//
//  1. 普通声明的函数只看得到普通声明 —— 引用 import / export 绑定会在**跑到那条
//     分支时才**抛 `ReferenceError`。本文件踩到的是 `parseQuote → parseInline`、
//     `parseList → listItemInfo`、`parseTable → splitRow`、`quoteBarHeight → estimateLines`。
//  2. `export` 声明**不提升**：只能调用写在它前面的 export 函数。本文件整体是
//     "自底向上"（字符工具 → 行内 → 块级 → 渲染），就是按这条排的。
//
// 所以：顶层函数一律 `export function`，且**被调方写在调用方上面**。
