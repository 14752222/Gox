// 画廊截图: gx-datav 单组件 —— RoseChart（玫瑰图 (南丁格尔)）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { RoseChart, palette } from "../datav/index.js";

const ROSE = [
  { name: "搜索", value: 4520 }, { name: "直访", value: 3120 },
  { name: "广告", value: 2180 }, { name: "社交", value: 1640 },
  { name: "邮件", value: 890 },
];

render(
  <window title="gx-datav · RoseChart" width={360} height={360} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <RoseChart size={280} data={ROSE}/>
      </row>
    </column>
  </window>
);
