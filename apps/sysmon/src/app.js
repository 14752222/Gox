// 根组件：页头 + 工具栏 + 折线图 + 指标卡片 + 侧栏。
//
// ⚠️ 导入绑定在组件体里别名一遍（C / sn / sm / …）—— 原因见 card.js 文件头：
// 跨模块组件的响应式闭包直接引用模块顶层导入绑定，重渲染时会失败。
import { h } from "gox";
import { colors, space, font } from "./theme.js";
import { Toolbar } from "./components/toolbar.js";
import { Chart } from "./components/chart.js";
import { Card } from "./components/card.js";
import { Sidebar } from "./components/sidebar.js";
import { CHANNELS, samples, paused, snap } from "./store.js";

export function App() {
  // —— 别名到激活作用域（勿删）——
  const C = colors;
  const S = space;
  const F = font;
  const chans = CHANNELS;
  const sm = samples;
  const pau = paused;
  const sn = snap;

  const Header = () => (
    <row gap={S.sm} padding={S.sm} background={C.panel} alignItems="center">
      <text font={F.lg} color={C.text}>系统资源监视器</text>
      <text font={F.xs} color={C.muted}>Gox 运行时资源 · 数据源 gx/dev（帧 / 缓存 / 树 / effect / 告警）</text>
      <spacer flexGrow={1} />
      <text font={F.xs} color={C.muted}>
        {() => (sn() ? "累计帧 " + sn().frame.count : "等待首帧")}
      </text>
    </row>
  );

  const cards = [];
  for (let i = 0; i < chans.length; i = i + 1) {
    cards.push(<Card channel={chans[i]} width={232} />);
  }

  return (
    <column gap={S.sm} padding={S.sm} background={C.bg}>
      <Header />
      <Toolbar />

      <row gap={S.md}>
        <column gap={S.sm} width={720}>
          <Chart width={720} height={244} />
          <grid columns={3} gap={S.sm}>{cards}</grid>
        </column>
        <Sidebar width={300} />
      </row>

      <row gap={S.md} padding={S.sm} background={C.panel}>
        <text font={F.xs} color={C.muted}>
          {() => (pau() ? "采样已暂停" : "每 500ms 采样一次") + " · 历史 " + sm() + " 点 / 容量 120"}
        </text>
        <spacer flexGrow={1} />
        <text font={F.xs} color={C.muted}>点击卡片或工具栏下拉切换曲线通道</text>
      </row>
    </column>
  );
}
