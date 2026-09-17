// P1-2 演示: 条件渲染 —— 三个 tab 用"函数子节点返回元素"切换内容。
// 运行: go run . testdata/tabs_demo.js
// 现象: 点击 Tab A/B/C, 下方内容整块替换 (标题 + 色块 + 说明文字都跟着换)。
//   - 函数子节点的求值结果可以是元素 / 数组 / 标量 / null;
//   - v1 不做 diff/key: 每次切换都整组重建子树 (旧子树的 effect 会被注销);
//   - 面板元素在每次求值里新建, 因此不会被"同一对象复用"的快捷路径命中,
//     这是刻意的 —— 演示的就是完整重建语义。
import { createSignal } from "gx/solid";
import { h, window, render } from "gx/gfx";

const [tab, setTab] = createSignal(0);

const panel = (title, color, desc) => (
  <column gap={6}>
    <text font={16}>{title}</text>
    <rect width={360} height={48} background={color}/>
    <text>{desc}</text>
  </column>
);

const tabButton = (index, label) => (
  <button
    background={() => (tab() === index ? "#1a5fb4" : "#e8e8e8")}
    color={() => (tab() === index ? "#ffffff" : "#1a1a1a")}
    onClick={() => setTab(index)}
  >{label}</button>
);

render(
  <column gap={12} padding={16}>
    <text font={18}>Conditional rendering</text>

    <row gap={8}>
      {tabButton(0, "Tab A")}
      {tabButton(1, "Tab B")}
      {tabButton(2, "Tab C")}
    </row>

    <text>{() => `active tab: ${tab()}`}</text>

    {() => (
      tab() === 0 ? panel("Panel A", "#c0392b", "red panel rendered from a function child")
      : tab() === 1 ? panel("Panel B", "#27ae60", "green panel rendered from a function child")
      : panel("Panel C", "#1a5fb4", "blue panel rendered from a function child")
    )}
  </column>,
  window({ title: "Tabs demo", width: 420, height: 280 })
);
