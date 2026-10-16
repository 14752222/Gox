// 画廊截图: gx-datav 单组件 —— TitleBar（装饰标题 (左 / 中两种)）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { TitleBar, palette } from "../datav/index.js";


render(
  <window title="gx-datav · TitleBar" width={340} height={200} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <column gap={18}>
            <TitleBar title="运营数据中心" subtitle="REAL-TIME OPERATIONS" fontSize={22} align="left"/>
            <TitleBar title="Gox 数据可视化大屏" align="center" fontSize={22}/>
          </column>
      </row>
    </column>
  </window>
);
