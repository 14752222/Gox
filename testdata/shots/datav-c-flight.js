// 画廊截图: gx-datav 单组件 —— FlightLines（飞线图）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { FlightLines, palette } from "../datav/index.js";

const LINES = [
  { from: [80, 300], to: [300, 90], value: 128 },
  { from: [120, 330], to: [430, 150], value: 96 },
  { from: [300, 90], to: [490, 280], value: 64 },
  { from: [430, 150], to: [180, 180], value: 42 },
  { from: [80, 300], to: [490, 280], value: 31 },
];
const LABELS = [
  { x: 66, y: 318, text: "华南仓" }, { x: 276, y: 66, text: "华东枢纽" },
  { x: 404, y: 168, text: "华北仓" }, { x: 462, y: 300, text: "西北仓" },
  { x: 156, y: 196, text: "西南仓" },
];

render(
  <window title="gx-datav · FlightLines" width={760} height={500} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <FlightLines width={560} height={380} lines={LINES} labels={LABELS}/>
      </row>
    </column>
  </window>
);
