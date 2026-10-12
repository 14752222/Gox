// 根组件：设备信息行 + 电池/网络状态卡 + CPU/内存实时折线。
import { h } from "gox";
import { colors, space, font } from "./theme.js";
import { cpu, mem, infoLine, batteryText, batteryOk, networkText, networkOk } from "./store.js";
import { linePoints } from "./lib/ring.js";

// StatCard: 一张状态卡（标题 + 大数字，ok() 驱动颜色）。
const StatCard = (p) => {
  return (
    <column gap={space.xs} padding={space.md} background={colors.panel} width={220}>
      <text font={font.sm} color={colors.muted}>{p.title}</text>
      <text font={font.xl} color={() => (p.ok() ? colors.ok : colors.bad)}>{p.text}</text>
    </column>
  );
}

// Chart: 一块折线画布。onDraw 里读 values() —— signal 变化自动重绘。
const Chart = (p) => {
  return (
    <column gap={space.xs}>
      <row gap={space.sm} alignItems="center">
        <text font={font.md} color={colors.text}>{p.title}</text>
        <text font={font.sm} color={p.color}>
          {() => {
            const vs = p.values();
            return vs.length > 0 ? vs[vs.length - 1].toFixed(1) + " %" : "--";
          }}
        </text>
      </row>
      <canvas
        width={340}
        height={140}
        background={colors.panel}
        onDraw={(ctx) => {
          const w = ctx.width;
          const hgt = ctx.height;
          // 网格线 25/50/75%
          for (let g = 1; g <= 3; g++) {
            const y = (hgt * g) / 4;
            ctx.line(0, y, w, y, colors.grid);
          }
          const pts = linePoints(p.values(), w, hgt, 100);
          for (let i = 1; i < pts.length; i++) {
            ctx.line(pts[i - 1].x, pts[i - 1].y, pts[i].x, pts[i].y, p.color);
          }
        }}
      />
    </column>
  );
}

export function App() {
  return (
    <column background={colors.bg} gap={space.md} padding={space.md}>
      <row gap={space.sm} alignItems="center">
        <text font={font.lg} color={colors.text}>系统监控</text>
        <spacer flexGrow={1} />
        <text font={font.sm} color={colors.muted}>{infoLine}</text>
      </row>
      <row gap={space.md}>
        <StatCard title="电池" text={batteryText} ok={batteryOk} />
        <StatCard title="网络" text={networkText} ok={networkOk} />
        <column gap={space.xs} padding={space.md} background={colors.panel} width={220}>
          <text font={font.sm} color={colors.muted}>采样</text>
          <text font={font.xl} color={colors.accent}>1 Hz</text>
        </column>
      </row>
      <row gap={space.md}>
        <Chart title="CPU" color={colors.accent} values={cpu} />
        <Chart title="内存" color={colors.warn} values={mem} />
      </row>
    </column>
  );
}
