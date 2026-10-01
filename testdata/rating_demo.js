// T08 演示: 星级评分 <rating> —— 完全受控 (显示看 value, 点击派发 onChange)。
// 运行: ./gox testdata/rating_demo.js
//
// 现象:
//   - 上面一排星星: 前 value 颗实心 (主题强调色), 其余空心描边;
//   - 点第几格就派发 onChange({value: 第几颗}), 日志追加一行, 星星跟着变;
//   - 点当前值的同一格是 no-op (不派发, 与分页器同款);
//   - 下一排是 max=10 的变体与只读展示 (disabled: 不可点)。
//
// 注意: model 指令对 <rating> 的读写口径与 <select> 相同 (读 value / 写 onChange)。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";

const [score, setScore] = createSignal(3);
const [log, setLog] = createSignal("(nothing yet)");
const push = (line) => setLog((prev) => (prev === "(nothing yet)" ? line : prev + "\n" + line));

render(
  h("column", { gap: 10, padding: 16 },
    h("rating", {
      value: score,
      onChange: (e) => { setScore(e.value); push("onChange -> " + e.value); },
    }),

    h("rating", { value: 7, max: 10, width: 200, color: "#e01b24" }),
    h("rating", { value: 2, disabled: true }),

    h("separator", { height: 1 }),
    h("text", { font: 12 }, () => "score: " + score()),
    h("text", { font: 12, wrap: true, color: "#555555" }, () => log())
  ),
  { title: "Rating demo", width: 340, height: 240 }
);
