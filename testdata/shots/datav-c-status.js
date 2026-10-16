// 画廊截图: gx-datav 单组件 —— StatusLight（状态指示灯组）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { StatusLight, palette } from "../datav/index.js";

const SERVICES = [
  { name: "API 网关", status: "ok", detail: "p99 38ms" },
  { name: "支付核心", status: "ok", detail: "p99 62ms" },
  { name: "搜索服务", status: "warn", detail: "延迟升高" },
  { name: "消息队列", status: "ok", detail: "堆积 0" },
  { name: "推荐引擎", status: "bad", detail: "实例 2/5 失联" },
  { name: "数据同步", status: "ok", detail: "滞后 1.2s" },
];

render(
  <window title="gx-datav · StatusLight" width={190} height={300} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <StatusLight width={168} items={SERVICES}/>
      </row>
    </column>
  </window>
);
