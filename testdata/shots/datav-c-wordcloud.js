// 画廊截图: gx-datav 单组件 —— WordCloud（词云 (螺旋布局)）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { WordCloud, palette } from "../datav/index.js";

const WORDS = [
  { text: "转化率", weight: 100 }, { text: "留存", weight: 82 },
  { text: "GMV", weight: 76 }, { text: "复购", weight: 64 },
  { text: "拉新", weight: 58 }, { text: "客单价", weight: 52 },
  { text: "ROI", weight: 47 }, { text: "活跃", weight: 41 },
  { text: "漏斗", weight: 36 }, { text: "标签", weight: 31 },
];

render(
  <window title="gx-datav · WordCloud" width={340} height={420} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <WordCloud width={300} height={320} words={WORDS}/>
      </row>
    </column>
  </window>
);
