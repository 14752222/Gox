// 画廊截图: gx-datav 单组件 —— FlipBoard（数字翻牌器）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { FlipBoard, palette } from "../datav/index.js";


render(
  <window title="gx-datav · FlipBoard" width={400} height={160} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <FlipBoard value={3842179} digits={8} size={30} accent={palette.warn}/>
      </row>
    </column>
  </window>
);
