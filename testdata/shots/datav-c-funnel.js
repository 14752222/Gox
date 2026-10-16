// 画廊截图: gx-datav 单组件 —— FunnelChart（漏斗图）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { FunnelChart, palette } from "../datav/index.js";

const FUNNEL = [
  { name: "曝光", value: 128400 }, { name: "点击", value: 38200 },
  { name: "加购", value: 12400 }, { name: "下单", value: 5180 },
  { name: "支付", value: 2960 },
];

render(
  <window title="gx-datav · FunnelChart" width={560} height={360} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <FunnelChart width={440} data={FUNNEL}/>
      </row>
    </column>
  </window>
);
