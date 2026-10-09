// Markdown 预览 —— 把 `lib/markdown.js` 产出的**节点描述树**物化成真的 gfx 节点。
//
// ## 这一层为什么必须存在
//
// `lib/` 不许 import `gox`（apps/README 第 4 条），所以 markdown.js 只能吐出
// `{ tag, props, children }` 这种"延后一层的 JSX"。真正调 `h()` 的活落在这里 ——
// 于是同一棵描述树既能被本文件物化成界面，也能被 `demo/probe.js` 直接断言结构。
//
// ## 响应式怎么接上
//
// 组件体**只在挂载时跑一次**；要跟着 signal 变，就得把求值放进**函数子节点**
// （`{() => …}`，每次重算都新建元素）。所以这里不是"算出树再返回"，而是
// "返回一个会重新算树的函数"。写成快照的后果是改了正文界面不动，且不报错。
//
// ## ⚠️ 末尾那张 export 表不是可选的
//
// 本文件所有顶层函数**都进了末尾的 `export {}`**。原因见 README「引擎限制」一节：
// Gox 的模块里，**未导出的函数看不见 import / export 绑定**（`materialize` 要用
// `h`、`styleFromTheme` 要用 `markdownStyle`，两者都是 import 进来的）。
import { h } from "gox";
import { renderMarkdown } from "../lib/markdown.js";
import { markdownStyle } from "../theme.js";

// styleFromTheme: theme.js 的排版表 → renderMarkdown 的 style 入参。
//
// 两份形状**刻意不一样**：theme.js 是"设计视角"（按块分组，颜色跟在每级标题上），
// renderMarkdown 要的是"渲染视角"（扁平、可缺省）。这个适配函数就是两者之间
// 唯一的那道缝 —— 换主题只改 theme.js。
export function styleFromTheme() {
  const m = markdownStyle;
  const heads = [];
  for (let i = 0; i < m.head.length; i++) heads.push(m.head[i].font);
  return {
    width: m.width,
    font: m.body.font,
    lineHeight: m.body.lineHeight,
    color: m.body.color,
    headFont: heads,
    headColor: m.head[0].color,
    codeFont: m.code.font,
    codeColor: m.code.color,
    codeBg: m.code.background,
    codeFamily: m.code.fontFamily,
    quoteColor: m.quote.color,
    quoteBar: m.quote.bar,
    linkColor: m.link,
    hrColor: m.hr,
    listGap: m.listGap,
    blockGap: m.blockGap,
  };
}

// materialize: 描述树 → GuiNode。
//
// 字符串叶子原样返回（h 会把它渲染成文本节点）；`#text` 是 markdown.js 用来
// 占位空节点的标签，props 为 null 时也走同一条路。
export function materialize(node) {
  if (node === null || node === undefined) return null;
  if (typeof node === "string") return node;
  const src = node.children || [];
  const kids = [];
  for (let i = 0; i < src.length; i++) {
    const k = materialize(src[i]);
    if (k !== null && k !== undefined) kids.push(k);
  }
  return h(node.tag, node.props, ...kids);
}

// MarkdownView: `source` 收**取值函数**（signal 本身就是函数）；传字符串也能跑，
// 只是不再响应式 —— 探针脚本里就是这么用的。
export function MarkdownView(p) {
  const read =
    typeof p.source === "function" ? p.source : () => (p.source === undefined ? "" : String(p.source));
  const style = styleFromTheme();
  return (
    <scroll width={p.width} height={p.height} background={p.background}>
      <column padding={12}>{() => materialize(renderMarkdown(read(), style))}</column>
    </scroll>
  );
}
