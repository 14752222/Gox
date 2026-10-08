// 数值卡片：一个通道的当前读数 + 滚动统计（走真实 stats.describe）+ 告警色点。
//
// 点击卡片即把折线图切到该通道（onClick 挂在容器上，hittest 对任意带 onClick
// 的节点生效）。顶部工具栏的 select 是同一动作的第二种入口。
//
// ⚠️ 为什么把导入绑定在组件体里**再别名一遍**（C / rev / lat / …）？
//   跨模块 import 的组件，其响应式闭包（函数子节点 / 函数 prop）若直接引用
//   模块顶层的导入绑定，**重渲染时会失败**（初始渲染正常、随后被吞成空白）——
//   与 apps-notes.md「status-bar」那条同源。别名到组件激活作用域即可绕开。
//   本应用实测：直接引用时卡片数值一行整行消失，别名后恢复。改这个文件时
//   **别把这些别名删掉**。
import { h } from "gox";
import { colors, space, font, levelColor } from "../theme.js";
import { selected, revision, latestFor, statsFor, levelFor, selectChannel } from "../store.js";

export const Card = (p) => {
  // —— 别名到激活作用域（勿删）——
  const ch = p.channel;
  const C = colors;
  const S = space;
  const F = font;
  const lc = levelColor;
  const sel = selected;
  const rev = revision;
  const lat = latestFor;
  const stFor = statsFor;
  const lv = levelFor;
  const pick = selectChannel;

  const fmt1 = (v) => (typeof v !== "number" || v !== v ? "—" : String(Math.round(v * 10) / 10));

  return (
    <column
      gap={S.xs}
      padding={S.sm}
      width={p.width}
      background={() => (sel() === ch.id ? C.panelAlt : C.panel)}
      onClick={() => pick(ch.id)}
    >
      <row gap={S.sm} alignItems="center">
        <rect width={8} height={8} background={() => lc(lv(ch.id))} />
        <text font={F.sm} color={C.text}>{ch.name}</text>
        <spacer flexGrow={1} />
        <text font={F.xs} color={C.muted}>{ch.unit}</text>
      </row>

      <text font={F.xl} color={() => lc(lv(ch.id))}>
        {() => {
          rev();
          return fmt1(lat(ch.id));
        }}
      </text>

      <text font={F.xs} color={C.muted}>
        {() => {
          rev();
          const st = stFor(ch.id);
          if (!st) return "样本 0";
          return "均值 " + fmt1(st.mean) + " · 低 " + fmt1(st.min) + " · 高 " + fmt1(st.max) + " · n=" + st.count;
        }}
      </text>
    </column>
  );
};
