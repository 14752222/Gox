// 画廊截图: gx-datav 单组件 —— ClockPanel（时钟面板 (Temporal 驱动)）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { ClockPanel, palette } from "../datav/index.js";

// 时间类组件：截图里是渲染那一刻的时钟，不可复现（见 README）。

render(
  <window title="gx-datav · ClockPanel" width={360} height={180} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <ClockPanel size={38}/>
      </row>
    </column>
  </window>
);
