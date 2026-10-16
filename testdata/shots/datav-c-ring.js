// 画廊截图: gx-datav 单组件 —— RingProgress（环形进度）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { RingProgress, palette } from "../datav/index.js";


render(
  <window title="gx-datav · RingProgress" width={280} height={280} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <RingProgress value={78} size={180} label="订单" sub="完成率"/>
      </row>
    </column>
  </window>
);
