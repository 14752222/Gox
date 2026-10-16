// 画廊截图: gx-datav 单组件 —— RankingList（排行榜 (奖牌徽章)）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { RankingList, palette } from "../datav/index.js";

const RANKS = [
  { name: "华南大区", value: 9821 }, { name: "华东大区", value: 8764 },
  { name: "华北大区", value: 7932 }, { name: "西南大区", value: 6418 },
  { name: "西北大区", value: 5210 }, { name: "东北大区", value: 4387 },
];

render(
  <window title="gx-datav · RankingList" width={600} height={380} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <RankingList width={480} rows={RANKS} rowHeight={40}/>
      </row>
    </column>
  </window>
);
