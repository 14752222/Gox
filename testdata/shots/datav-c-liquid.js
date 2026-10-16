// 画廊截图: gx-datav 单组件 —— LiquidFill（水球图）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { LiquidFill, palette } from "../datav/index.js";


render(
  <window title="gx-datav · LiquidFill" width={280} height={320} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <LiquidFill value={86} size={200} label="存储"/>
      </row>
    </column>
  </window>
);
