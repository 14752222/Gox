// P1-2 演示: 列表渲染 —— 按钮向数组 signal 增删元素, 列表即时增减。
// 运行: go run . testdata/list_demo.js
// 现象: Add 追加一行, Remove 删掉最后一行, 计数同步刷新。
//   - 函数子节点返回数组 → 逐元素挂载; 数组挂在 column 里就竖排, 挂在 row
//     里就横排 (slot 的排布方向跟随父容器), 间距沿用父容器的 gap;
//   - v1 不做 diff/key: 数组变化整组重建, 长列表的增量更新留待后续版本。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";

const [items, setItems] = createSignal(["alpha", "beta"]);
let serial = 2;

function add() {
  serial = serial + 1;
  setItems(list => list.concat([`item ${serial}`]));
}

function remove() {
  setItems(list => (list.length === 0 ? list : list.slice(0, list.length - 1)));
}

const row = (text) => (
  <row gap={8} alignItems="center">
    <rect width={10} height={10} background="#27ae60"/>
    <text>{text}</text>
  </row>
);

render(
  <window title="List demo" width={400} height={320}>
    <column gap={10} padding={16}>
      <text font={18}>List rendering</text>

      <row gap={8}>
        <button onClick={add}>Add</button>
        <button onClick={remove}>Remove</button>
      </row>

      <text>{() => `count: ${items().length}`}</text>
      <separator/>

      <column gap={6}>
        {() => items().map(row)}
      </column>
    </column>
  </window>
);
