// S4 演示: 树形控件 (tree) —— 层级 / 展开收起 / 缩进 / 选中回调。
// 运行: ./gox testdata/tree_demo.js
// 现象: 一棵文件树初始全收起; 点 "src" 展开出两个子节点 (带缩进),
//   再点收回; 展开 src 后再展开 gfx, 两边各自保持展开 (互不影响);
//   点任意节点下方显示"选中了 xxx"。
//   - nodes 递归数据 ({label, children}), 纯字符串数组当叶子简写;
//   - 展开态是渲染层状态 (不用脚本回写), 点有子节点的行即切换;
//   - 缩进按层级算, 叶子保留箭头占位所以同层文字左对齐;
//   - onSelect 收到被点节点 {label, key, leaf, index}。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";

const TREE = [
  {
    label: "src",
    children: [
      {
        label: "gfx",
        children: [{ label: "node.go" }, { label: "layout.go" }, { label: "table.go" }],
      },
      { label: "main.go" },
    ],
  },
  {
    label: "testdata",
    children: [{ label: "table_demo.js" }, { label: "tree_demo.js" }],
  },
  { label: "README.md" },
];

const [picked, setPicked] = createSignal("-");

render(
  <window title="Tree demo" width={420} height={360}>
    <column gap={10} padding={16}>
      <text font={18}>Tree</text>

      <scroll height={240}>
        <tree nodes={TREE} onSelect={(n) => setPicked(n.label)} />
      </scroll>

      <text>{() => `选中: ${picked()}`}</text>
    </column>
  </window>
);
