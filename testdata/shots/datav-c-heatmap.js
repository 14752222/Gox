// 画廊截图: gx-datav 单组件 —— HeatMap（热力图 (7 × 24)）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { HeatMap, palette, makeRng } from "../datav/index.js";

const HOURS = ["00", "03", "06", "09", "12", "15", "18", "21", "24"];
const DAYS = ["周一", "周二", "周三", "周四", "周五", "周六", "周日"];
function makeHeat() {
  const rng = makeRng(77);
  const out = [];
  for (let d = 0; d < 7; d++) {
    const row = [];
    for (let h = 0; h < 24; h++) {
      const base = 40 * Math.exp(-Math.pow((h - 11) / 4.5, 2)) + 55 * Math.exp(-Math.pow((h - 20) / 3, 2));
      row.push(Math.round(Math.max(2, base * (0.55 + rng() * 0.7))));
    }
    out.push(row);
  }
  return out;
}
const HEATDATA = makeHeat();

render(
  <window title="gx-datav · HeatMap" width={800} height={360} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <HeatMap width={720} height={240} cellH={26} data={HEATDATA} xLabels={HOURS} yLabels={DAYS}/>
      </row>
    </column>
  </window>
);
