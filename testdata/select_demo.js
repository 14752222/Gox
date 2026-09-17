// P2-3 演示: 下拉框 —— 展开弹层 / 点击选择 / 点击外部收起。
// 运行: go run . testdata/select_demo.js
// 现象: 点第一个下拉展开四个城市的选项 (弹层溢出 select 的 28px 盒子显示,
//   不被裁剪也不被后面的兄弟盖住); 悬停高亮, 点击即选中并收起;
//   把下拉展开后点画面别处, 弹层收起且这次点击不会顺带按到别的控件。
//   - options 支持字符串数组与 {value, label} 数组;
//   - 受控: 选中只派发 onChange, 显示值取决于 value prop (由 signal 驱动);
//   - 键盘: 聚焦后 Enter/Space 展开, 上下键移动高亮, Enter 选中, Esc 收起。
import { createSignal } from "gx/solid";
import { h, window, render } from "gx/gfx";

const CITIES = [
  { value: "sh", label: "Shanghai" },
  { value: "bj", label: "Beijing" },
  { value: "sz", label: "Shenzhen" },
  { value: "hz", label: "Hangzhou" },
];

const [city, setCity] = createSignal("sh");
const [fruit, setFruit] = createSignal("");

render(
  <column gap={10} padding={16}>
    <text font={18}>Select</text>

    <select
      width={220}
      options={CITIES}
      value={() => city()}
      onChange={(e) => setCity(e.value)}
    />

    <select
      width={220}
      placeholder="Pick a fruit"
      options={["apple", "banana", "cherry"]}
      value={() => fruit()}
      onChange={(e) => setFruit(e.value)}
    />

    <text>{() => `city = ${city()}   fruit = ${fruit() || "-"}`}</text>
  </column>,
  window({ title: "Select demo", width: 380, height: 260 })
);
