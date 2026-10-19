// 画廊截图: <table> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// 运行: ./gox testdata/shots/table.js
//
// 三张表放一屏: 斑马纹开/关、borderless、以及带对齐的列宽 —— 斑马纹的实现
// 从"边框"改成"底色"（Prism S5）时，这三张图的差异就是评审依据。
import { render } from "gox";

const columns = ["组件", "数量", "状态"];
const rows = [
  ["button", 12, "在库"],
  ["input", 0, "缺货"],
  ["select", 7, "在库"],
];

render(
  <window title="table" width={420} height={420}>
    <column gap={16} padding={16}>
      <text>斑马纹</text>
      <table columns={columns} rows={rows} zebra={true} />
      <text>无斑马纹 + 无边框</text>
      <table columns={columns} rows={rows} borderless={true} />
      <text>表头自定义底色</text>
      <table columns={columns} rows={rows} headerBackground="#eef3fa" zebra={true} />
    </column>
  </window>
);
