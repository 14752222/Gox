// S4 演示: 数据表格 (table) —— 表头 / 对象行 / 列配置 / 斑马纹 / 行点击。
// 运行: go run . testdata/table_demo.js
// 现象: 三列文件表 (名称/大小/类型), 第 2/4 行有浅灰斑马纹; 鼠标划过某行
//   整行泛蓝 (悬停高亮), 点一行下方文字显示"选中第几行";
//   右侧是无网格线的紧凑表格 (borderless), 数字列右对齐。
//   - columns 支持字符串数组 (简写) 与 {key,label,width,align} 数组;
//   - rows 支持二维数组 (按列下标取) 与对象数组 (按 key 取);
//   - 行高固定 28, 与 select/input 对齐; 列宽未显式给定时按内容比例分配;
//   - onRowClick 收到 {index, row}, 不挂它表格就是纯展示 (行不可点)。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";

const FILES = [
  { name: "main.go", size: "1.2KB", kind: "Go" },
  { name: "node.go", size: "8.4KB", kind: "Go" },
  { name: "logo.png", size: "48KB", kind: "图片" },
  { name: "README.md", size: "3.1KB", kind: "文档" },
];

const [picked, setPicked] = createSignal("-");

render(
  <window title="Table demo" width={560} height={340}>
    <column gap={12} padding={16}>
      <text font={18}>Table</text>

      {/* 主表格: 带斑马纹, 行可点 */}
      <table
        width={520}
        zebra
        columns={[
          { key: "name", label: "名称" },
          { key: "size", label: "大小", width: 90, align: "right" },
          { key: "kind", label: "类型", width: 80 },
        ]}
        rows={FILES}
        onRowClick={(e) => setPicked(`选中第 ${e.index + 1} 行: ${e.row.name}`)}
      />

      <text>{() => picked()}</text>

      {/* 紧凑表格: 无网格线, 纯展示 (不挂 onRowClick, 行不可点也不变色) */}
      <table
        width={320}
        borderless
        columns={["页面", "耗时"]}
        rows={[
          ["首页", "12ms"],
          ["详情", "38ms"],
        ]}
      />
    </column>
  </window>
);
