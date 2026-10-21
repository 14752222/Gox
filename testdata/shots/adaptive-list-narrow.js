// 截图基线：README 首屏示例的**单栏**形态（<600dp）。
// 与 adaptive-list-wide.js 是同一页的两个定格，对照着看就是「单双栏自适应」。
import { h, render } from "gx/gfx";

const ITEMS = [
  { id: "a", title: "编译管线", desc: "词法 → 语法 → 字节码 → 栈式 VM，109 个操作码。" },
  { id: "b", title: "软件光栅化 GUI", desc: "flex 布局 + JSX + 信号驱动，三后端。" },
  { id: "c", title: "脚本打包器", desc: "把 .js 打成单个可执行文件。" },
];

render(
  <window title="自适应列表页 · 单栏" width={480} height={420}>
    <column gap={10} padding={14}>
      <text font={15}>breakpoint = sm   →   单栏</text>
      <column gap={8}>
        <view each={ITEMS} key="id">
          {(it, i) => (
            <row gap={6} padding={8} radius={6} background={i === 0 ? "#dce8ff" : "#f2f5fa"}>
              <text font={13} color="#334">{it.title}</text>
            </row>
          )}
        </view>
        <column gap={6} padding={12} radius={8} background="#ffffff">
          <text font={14} color="#223">{ITEMS[0].title}</text>
          <text font={12} color="#667">{ITEMS[0].desc}</text>
        </column>
      </column>
    </column>
  </window>
);
