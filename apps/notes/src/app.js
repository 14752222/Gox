// 根组件：标题行（含预览/图表切换）+ 左列表 + 右上预览/图表 + 右下正文编辑 + 状态行。
//
// 布局刻意**不套窗口尺寸**：内层给了固定宽高，窗口 800×640 是"刚好装下"的结果，
// 换更小的屏（Gox 是多屏多设备运行时）靠改这几个数字，不是靠布局自动适应 ——
// 自动适应的部分在 `lib/` 里（drawChart 不写 width/height 就按画布尺寸画）。
import { h } from "gox";
import { colors, space, font } from "./theme.js";
import { NoteList } from "./components/note-list.js";
import { MarkdownView } from "./components/markdown-view.js";
import { ChartView } from "./components/chart-view.js";
import {
  tab,
  setTab,
  status,
  current,
  currentBody,
  currentChart,
  setBody,
} from "./store.js";

// TabButton: 选中态读 tab() —— 写在**函数体**里而不是组件体里，否则只取第一帧。
export function TabButton(p) {
  return (
    <button
      padding={6}
      background={() => (tab() === p.name ? colors.accent : colors.panel)}
      color={() => (tab() === p.name ? colors.accentFg : colors.text)}
      onClick={() => setTab(p.name)}
    >{p.label}</button>
  );
}

export function titleText() {
  const n = current();
  return n === null ? "（没有笔记）" : n.title;
}

export function App() {
  return (
    <column background={colors.bg}>
      <row gap={space.sm} padding={space.sm} alignItems="center">
        <text font={font.lg} color={colors.text}>{titleText}</text>
        <spacer flexGrow={1} />
        <TabButton name="preview" label="预览" />
        <TabButton name="chart" label="图表" />
      </row>

      <row gap={space.md} padding={space.md}>
        <NoteList />
        <column gap={space.sm}>
          <view show={() => tab() === "preview"}>
            <MarkdownView source={currentBody} width={540} height={330} background={colors.panel} />
          </view>
          <view show={() => tab() === "chart"}>
            <ChartView cfg={currentChart} width={540} height={330} />
          </view>
          <column gap={space.xs}>
            <text font={font.xs} color={colors.muted}>正文（改了上面立刻跟着变）</text>
            <textarea
              width={540}
              height={140}
              font={font.sm}
              fontFamily="monospace"
              value={currentBody}
              onInput={(e) => setBody(e.value)}
            />
          </column>
        </column>
      </row>

      <row padding={space.sm}>
        <text font={font.xs} color={colors.muted}>{status}</text>
      </row>
    </column>
  );
}
