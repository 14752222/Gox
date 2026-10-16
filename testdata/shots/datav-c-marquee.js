// 画廊截图: gx-datav 单组件 —— MarqueeText（滚动消息栏）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { MarqueeText, palette } from "../datav/index.js";

// 短消息 → 组件走静态分支（不滚），否则首帧文本在视口外，截图一片空。

render(
  <window title="gx-datav · MarqueeText" width={760} height={180} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <MarqueeText text="● 双十一预热进行中 · 库存同步任务 12/12 完成 · 支付通道延迟恢复正常" speed={70} fontSize={15} accent={palette.warn}/>
      </row>
    </column>
  </window>
);
