// 画廊截图: gx-datav 单组件 —— Countdown（倒计时）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { Countdown, palette } from "../datav/index.js";

const TARGET = Number(Temporal.Now.instant().epochMilliseconds) + 8 * 3600 * 1000 + 42 * 60 * 1000;

render(
  <window title="gx-datav · Countdown" width={380} height={200} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <Countdown target={TARGET} size={44}/>
      </row>
    </column>
  </window>
);
