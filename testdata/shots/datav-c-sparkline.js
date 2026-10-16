// 画廊截图: gx-datav 单组件 —— Sparkline（迷你趋势线 (无轴无刻度)）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { Sparkline, palette, seriesRandomWalk } from "../datav/index.js";

const TREND = seriesRandomWalk(32, 100, 22, 9);

render(
  <window title="gx-datav · Sparkline" width={560} height={240} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <column gap={16} alignItems="center">
            <Sparkline data={TREND} width={360} height={70}/>
            <Sparkline data={TREND} width={360} height={54} color={palette.ok} smooth={false}/>
          </column>
      </row>
    </column>
  </window>
);
