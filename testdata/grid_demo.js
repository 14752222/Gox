// 网格布局演示: <grid columns={n}> (§四 布局缺口, 2026-09-19)。
// 运行: go run . testdata/grid_demo.js
// 现象:
//   1. 六张卡片排成 3 列等宽网格, 行高随该行最高卡片; 卡片是容器, 自动
//      拉伸到列宽与行高 —— 等高卡片栅格零 JS。
//   2. 拖窄窗口: 网格被 column stretch 出确定宽度, 列宽 = (宽-2*gap)/3
//      连续收缩; 想要"放不下就变 2 列"再叠一层响应式 (onResize 切 columns)。
//   3. 每张卡片用了装饰批 (status §29) 的 radius/渐变/阴影 —— 两批能力组合。
//
// 词汇 (gui-component-status.md §30): columns={n} (钳 1..32)、gap/padding 照常;
// 格内: 显式宽/高用自己的, 容器与零高节点拉伸到行高, alignItems 两轴生效;
// 不做: 轨道语法 / colSpan / 区域命名 (不等宽列用 row + 百分比/min-max 组合)。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";

const [picked, setPicked] = createSignal(-1);

const cards = [
  { name: "alpha", from: "#6c8fd9", to: "#3355aa" },
  { name: "bravo", from: "#7ac9a3", to: "#2b7a4b" },
  { name: "charlie", from: "#e8b86d", to: "#b06a1a" },
  { name: "delta", from: "#e88f8f", to: "#b03a3a" },
  { name: "echo", from: "#b59ae0", to: "#5f3d99" },
  { name: "foxtrot", from: "#8fc7d9", to: "#2a7a99" },
];

render(
  <window title="grid demo" width={520} height={360}>
    <column gap={12} padding={16}>
      <text font={16}>{() => (picked() < 0 ? "pick a card" : `picked: ${cards[picked()].name}`)}</text>

      <grid columns={3} gap={10}>
        {cards.map((c, i) => (
          <column
            key={c.name}
            radius={10}
            background={`linear-gradient(to bottom, ${c.from}, ${c.to})`}
            shadow={{x: 0, y: 2, blur: 4, color: "#00000030"}}
            onClick={() => setPicked(i)}
          >
            <text font={14} color="#ffffff" padding={10}>{c.name}</text>
            <rect height={() => (picked() === i ? 6 : 0)} background="#ffffffcc" width="100%" />
          </column>
        ))}
      </grid>

      <text font={12} color="#889">
        equal-width columns, row height = tallest card; containers stretch to the cell.
      </text>
    </column>
  </window>
);
