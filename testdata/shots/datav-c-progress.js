// 画廊截图: gx-datav 单组件 —— ProgressList（进度条组）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { ProgressList, palette } from "../datav/index.js";

const PROGRESS = [
  { name: "存储", value: 78, max: 100 }, { name: "计算", value: 46, max: 100 },
  { name: "带宽", value: 91, max: 100 }, { name: "连接数", value: 33, max: 100 },
];

render(
  <window title="gx-datav · ProgressList" width={600} height={360} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <ProgressList width={480} rows={PROGRESS} rowHeight={34}/>
      </row>
    </column>
  </window>
);
