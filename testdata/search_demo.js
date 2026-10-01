// T08 演示: 搜索框 <search> —— input 的字段变体 (左侧放大镜 + Enter 提交)。
// 运行: ./gox testdata/search_demo.js
//
// 现象:
//   - 左侧有放大镜图标, 文字从图标右侧起排 (与 <input> 的差别只这一处外观);
//   - 逐键输入走 onInput (受控回写, 与 <input> 完全一致);
//   - 获焦时按 Enter 整段提交 onSearch({value}), 日志追加一行;
//   - 点 "清空" 把 query 置回空串 (受控: 显示跟着变)。
//
// 注意: model 指令对 <search> 的读写口径与 <input> 相同 (读 value / 写 onInput)。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";

const [query, setQuery] = createSignal("");
const [log, setLog] = createSignal("(nothing yet)");
const push = (line) => setLog((prev) => (prev === "(nothing yet)" ? line : prev + "\n" + line));

render(
  h("column", { gap: 10, padding: 16 },
    h("search", {
      model: query,
      placeholder: "搜一下…",
      width: 320,
      onSearch: (e) => push("search -> " + e.value),
    }),

    h("row", { gap: 8 },
      h("button", { onClick: () => setQuery("") }, "清空")
    ),

    h("separator", { height: 1 }),
    h("text", { font: 12, width: 340 }, () => "query: " + query()),
    h("text", { font: 12, width: 340, wrap: true, color: "#555555" }, () => log())
  ),
  { title: "Search demo", width: 380, height: 240 }
);
