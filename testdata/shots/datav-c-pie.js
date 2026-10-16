// 画廊截图: gx-datav 单组件 —— PieChart（饼图 / 环图 (悬停外扩)）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { PieChart, palette, seriesColors } from "../datav/index.js";

const PIE = [
  { name: "移动端", value: 4520, color: seriesColors[0] },
  { name: "PC 端", value: 3120, color: seriesColors[1] },
  { name: "小程序", value: 2180, color: seriesColors[2] },
  { name: "第三方", value: 980, color: seriesColors[3] },
];

render(
  <window title="gx-datav · PieChart" width={320} height={340} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <PieChart donut={true} size={240} data={PIE} center="8,080" unit=" 单"/>
      </row>
    </column>
  </window>
);
