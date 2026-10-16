// 画廊截图: gx-datav 单组件 —— KpiCard（KPI 指标卡 (含迷你趋势线)）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { KpiCard, palette, seriesRandomWalk } from "../datav/index.js";

const TREND = seriesRandomWalk(20, 100, 18, 3);

render(
  <window title="gx-datav · KpiCard" width={600} height={240} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <KpiCard label="今日 GMV" value={1284560} delta={12.4} trend={TREND} width={440}/>
      </row>
    </column>
  </window>
);
