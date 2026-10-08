// 侧栏：显示器 / 窗口 / 运行时资源 / 宿主 / 内核告警。
//
// 数据全部来自真实读数：gx/screen（显示器/窗口）、gx/dev（帧/缓存/树/solid/告警）、
// gx/device（电池/设备）、process（进程）。
//
// ⚠️ 两个本仓库实测的坑（别改回去）：
//   1. 组件标签的 children 在 parser 层是**位置参数** `Comp(props, ...children)`，
//      不会挂到 `props.children` 上 —— 所以这里不能用「接收 children 的面板组件」，
//      面板结构直接内联展开。
//   2. 响应式闭包直接引用模块顶层导入绑定会在重渲染时失败（见 card.js 文件头），
//      所以导入绑定一律先别名到组件激活作用域。
import { h } from "gox";
import { battery, deviceInfo } from "gx/device";
import { colors, space, font } from "../theme.js";
import { screenList, windowList, alerts, samples, snap, refreshTopology } from "../store.js";

export const Sidebar = (p) => {
  // —— 别名到激活作用域（勿删）——
  const C = colors;
  const S = space;
  const F = font;
  const sl = screenList;
  const wl = windowList;
  const al = alerts;
  const sm = samples;
  const sn = snap;
  const rtop = refreshTopology;
  const batt = battery;
  const dev = deviceInfo;

  // 面板标题行（局部辅助，直接调用展开，不走 children 机制）
  const Head = (title, note) => (
    <row gap={S.sm} alignItems="center">
      <text font={F.sm} color={C.text}>{title}</text>
      <spacer flexGrow={1} />
      <text font={F.xs} color={C.muted}>{note}</text>
    </row>
  );

  return (
    <column gap={S.md} width={p.width}>

      <column gap={S.xs} padding={S.sm} background={C.panel}>
        {Head("显示器", () => sl().length + " 块")}
        <view each={sl} key="id">
          {(s) => (
            <text font={F.xs} color={C.muted}>
              {s.name + "  " + s.width + "×" + s.height + "  scale " + s.scale + (s.primary ? "  主" : "")}
            </text>
          )}
        </view>
      </column>

      <column gap={S.xs} padding={S.sm} background={C.panel}>
        {Head("窗口", () => wl().length + " 个")}
        <view each={wl} key="id">
          {(w) => (
            <text font={F.xs} color={C.muted}>
              {w.id + ": " + (w.title === "" ? "(无题)" : w.title) + "  @" + w.displayId + (w.active ? " ●" : "")}
            </text>
          )}
        </view>
        <button padding={3} onClick={rtop}>刷新</button>
      </column>

      <column gap={S.xs} padding={S.sm} background={C.panel}>
        {Head("运行时资源", () => "样本 " + sm())}
        <text font={F.xs} color={C.muted}>累计帧 {() => { const s = sn(); return s ? String(s.frame.count) : "—"; }}</text>
        <text font={F.xs} color={C.muted}>{"整帧/局部分布 "}
          {() => { const s = sn(); return s ? s.frame.full + " / " + s.frame.partial : "—"; }}
        </text>
        <text font={F.xs} color={C.muted}>{"图像缓存 size/cap "}
          {() => { const s = sn(); return s ? s.imageCache.size + " / " + s.imageCache.cap : "—"; }}
        </text>
        <text font={F.xs} color={C.muted}>{"字形缓存 size/cap "}
          {() => { const s = sn(); return s ? s.glyphCache.size + " / " + s.glyphCache.cap : "—"; }}
        </text>
        <text font={F.xs} color={C.muted}>{"GUI 树 窗口/节点/深度 "}
          {() => { const s = sn(); return s ? s.tree.windows + " / " + s.tree.nodes + " / " + s.tree.depth : "—"; }}
        </text>
        <text font={F.xs} color={C.muted}>{"响应式 effect "}
          {() => { const s = sn(); return s ? String(s.solid.effects) : "—"; }}
        </text>
      </column>

      <column gap={S.xs} padding={S.sm} background={C.panel}>
        {Head("宿主", "process / gx/device")}
        <text font={F.xs} color={C.muted}>{() => "platform " + process.platform + "  pid " + process.pid}</text>
        <text font={F.xs} color={C.muted}>{"电池 / 供电 "}
          {() => {
            try {
              const b = batt();
              if (!b || !b.supported) return "不支持";
              const pct = b.levelPercent >= 0 ? b.levelPercent + "%" : "未知";
              return pct + (b.charging ? "  供电中(" + b.chargingType + ")" : "");
            } catch (e) {
              return "不可用";
            }
          }}
        </text>
        <text font={F.xs} color={C.muted}>{"设备 "}
          {() => {
            try {
              const d = dev();
              return d.os + " / " + d.arch;
            } catch (e) {
              return "—";
            }
          }}
        </text>
      </column>

      <column gap={S.xs} padding={S.sm} background={C.panel}>
        {Head("内核告警", () => al().length + " 条")}
        <view each={al} key="id">
          {(a) => <text font={F.xs} color={C.warn}>{a.at + "  " + a.text}</text>}
        </view>
        <text font={F.xs} color={C.muted}>{() => (al().length === 0 ? "（无告警）" : "")}</text>
      </column>

    </column>
  );
};
