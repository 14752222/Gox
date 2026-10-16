// 画廊截图: gx-datav 单组件 —— CountUp（数字缓动）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { CountUp, palette } from "../datav/index.js";


render(
  <window title="gx-datav · CountUp" width={300} height={190} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <column gap={14} alignItems="center">
            <CountUp value={128456} fontSize={30} color={palette.accent}/>
            <CountUp value={68.4} fontSize={22} decimals={1} suffix="%" color={palette.ok}/>
          </column>
      </row>
    </column>
  </window>
);
