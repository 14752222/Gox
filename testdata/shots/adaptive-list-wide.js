// 截图基线：README 首屏示例的**双栏**形态（>=600dp）。
// 完整自适应版本（拖拽宽度实时切换）在 testdata/adaptive_list_demo.js；
// 这张是静态定格，进 desktop-shots 工作流的截图基线（看板 rSm7zW：三端
// 截图对比基线入库，桌面侧落这条链）。
import { h, render } from "gx/gfx";

const ITEMS = [
  { id: "a", title: "编译管线", desc: "词法 → 语法 → 字节码 → 栈式 VM，109 个操作码。" },
  { id: "b", title: "软件光栅化 GUI", desc: "flex 布局 + JSX + 信号驱动，三后端。" },
  { id: "c", title: "脚本打包器", desc: "把 .js 打成单个可执行文件。" },
  { id: "d", title: "宿主能力模块", desc: "fs / http / clipboard / 对话框 / 持久化。" },
];

render(
  <window title="自适应列表页 · 双栏" width={760} height={420}>
    <column gap={10} padding={14}>
      <text font={15}>breakpoint = md   →   双栏</text>
      <text font={11} color="#667">thresholds (dp):  sm@0  md@600  lg@840  xl@1200</text>
      <row gap={10}>
        <column width={210} gap={6}>
          <view each={ITEMS} key="id">
            {(it, i) => (
              <row gap={6} padding={8} radius={6} background={i === 0 ? "#dce8ff" : "#f2f5fa"}>
                <text font={13} color="#334">{it.title}</text>
              </row>
            )}
          </view>
        </column>
        <column flex={1}>
          <column gap={6} padding={12} radius={8} background="#ffffff">
            <text font={14} color="#223">{ITEMS[0].title}</text>
            <text font={12} color="#667">{ITEMS[0].desc}</text>
          </column>
        </column>
      </row>
    </column>
  </window>
);
