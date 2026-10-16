// 画廊截图: gx-datav 单组件 —— RadarChart（雷达图）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { RadarChart, palette } from "../datav/index.js";

const RADAR = [
  { name: "本期", values: [82, 90, 68, 76, 88, 72] },
  { name: "上期", values: [64, 72, 80, 58, 66, 82] },
];
const AXES = ["订单", "履约", "库存", "售后", "营销", "风控"];

render(
  <window title="gx-datav · RadarChart" width={340} height={340} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <RadarChart size={280} axes={AXES} series={RADAR}/>
      </row>
    </column>
  </window>
);
