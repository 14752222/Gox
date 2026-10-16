// 画廊截图: gx-datav 单组件 —— BulletChart（子弹图 (实际 vs 目标)）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { BulletChart, palette } from "../datav/index.js";

const BULLET = [
  { name: "订单量", value: 86, target: 75, max: 100 },
  { name: "退单率", value: 12, target: 15, max: 100, unit: "%" },
  { name: "履约时效", value: 94, target: 90, max: 100, unit: "%" },
  { name: "客服满意度", value: 88, target: 92, max: 100, unit: "%" },
];

render(
  <window title="gx-datav · BulletChart" width={600} height={360} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <BulletChart width={480} rows={BULLET}/>
      </row>
    </column>
  </window>
);
