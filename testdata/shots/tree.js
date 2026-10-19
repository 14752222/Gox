// 画廊截图: <tree> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// 运行: ./gox testdata/shots/tree.js
//
// 展开态是运行时状态而不是 props: 点箭头才展开。tree 的节点是数据，
// 缺省全部收起 —— 收起时只有一行，拍不出"同级缩进 + 叶子占位对齐"这两处
// 最容易错位的几何。所以这里点开第一个箭头再截图。
import { render } from "gox";

const nodes = [
  { label: "src", children: [
    { label: "gfx", children: [
      { label: "render.go" },
      { label: "raster.go" },
    ]},
    { label: "vm" },
  ]},
  { label: "docs", children: [
    { label: "theme.md" },
  ]},
];

render(
  <window title="tree" width={360} height={280}>
    <column padding={16}>
      <tree nodes={nodes} width={300} />
    </column>
  </window>
);
