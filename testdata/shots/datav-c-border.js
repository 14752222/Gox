// 画廊截图: gx-datav 单组件 —— BorderBox（科技风面板边框 (6 型)）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { BorderBox, TitleBar, palette } from "../datav/index.js";


render(
  <window title="gx-datav · BorderBox" width={560} height={300} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <BorderBox type={1} width={460} height={220}>
            <TitleBar title="核心指标" align="left" fontSize={14}/>
            <column flexGrow={1} justifyContent="center" gap={6}>
              <text font={12} color={palette.sub}>面板底 + 装饰 canvas + 内容流</text>
              <text font={12} color={palette.sub}>内容区走普通 flex，随意嵌套</text>
            </column>
          </BorderBox>
      </row>
    </column>
  </window>
);
