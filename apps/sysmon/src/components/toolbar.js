// 工具栏：通道选择 + 暂停/继续 + 清空 + 多屏模式 + 刷新拓扑。
//
// ⚠️ 导入绑定在组件体里别名一遍（C / sel / pau / …）—— 原因见 card.js 文件头：
// 跨模块组件的响应式闭包直接引用模块顶层导入绑定，重渲染时会失败。
import { h } from "gox";
import { colors, space, font } from "../theme.js";
import {
  CHANNELS,
  selected,
  paused,
  selectChannel,
  togglePause,
  clearHistory,
  rebaseBaseline,
  refreshTopology,
} from "../store.js";
import { openScreenWindows } from "../multiscreen.js";

export const Toolbar = () => {
  // —— 别名到激活作用域（勿删）——
  const C = colors;
  const S = space;
  const F = font;
  const chans = CHANNELS;
  const sel = selected;
  const pau = paused;
  const pick = selectChannel;
  const tog = togglePause;
  const clr = clearHistory;
  const reb = rebaseBaseline;
  const rtop = refreshTopology;
  const openMulti = openScreenWindows;

  const OPTIONS = chans.map((c) => ({ value: c.id, label: c.name + "（" + c.unit + "）" }));

  return (
    <row gap={S.sm} padding={S.sm} background={C.panel} alignItems="center">
      <text font={F.sm} color={C.muted}>曲线通道</text>

      {/* select 受控：显示看 value，选中只派发 onChange */}
      <select
        width={190}
        options={OPTIONS}
        value={() => sel()}
        onChange={(e) => pick(e.value)}
      />

      <button padding={4} onClick={tog}>{() => (pau() ? "继续" : "暂停")}</button>

      <button
        padding={4}
        onClick={() => {
          clr();
          reb();
        }}
      >
        清空历史
      </button>

      <button padding={4} onClick={openMulti}>多屏模式</button>
      <button padding={4} onClick={rtop}>刷新拓扑</button>

      <spacer flexGrow={1} />
      <text font={F.sm} color={() => (pau() ? C.warn : C.ok)}>
        {() => (pau() ? "● 已暂停" : "● 实时采样")}
      </text>
    </row>
  );
};
