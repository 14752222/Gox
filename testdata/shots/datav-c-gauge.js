// 画廊截图: gx-datav 单组件 —— Gauge（仪表盘 (225° 三段刻度)）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { Gauge, palette } from "../datav/index.js";


render(
  <window title="gx-datav · Gauge" width={360} height={340} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <Gauge value={72} min={0} max={100} unit="%" label="综合负载" size={280}/>
      </row>
    </column>
  </window>
);
