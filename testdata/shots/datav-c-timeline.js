// 画廊截图: gx-datav 单组件 —— Timeline（时间轴）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { Timeline, palette } from "../datav/index.js";

const EVENTS = [
  { time: "16:58", title: "支付通道延迟恢复", detail: "延迟从 210ms 回落至 38ms", status: "ok" },
  { time: "16:41", title: "推荐引擎实例失联", detail: "node-07 / node-09 心跳超时", status: "bad" },
  { time: "16:20", title: "搜索服务延迟升高", detail: "p99 超过 500ms 阈值", status: "warn" },
  { time: "15:55", title: "大促预热活动上线", detail: "双十一主会场 · 全量发布", status: "info" },
  { time: "15:30", title: "库存同步任务完成", detail: "12/12 分片 · 零差异", status: "ok" },
];

render(
  <window title="gx-datav · Timeline" width={300} height={440} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <Timeline width={268} events={EVENTS}/>
      </row>
    </column>
  </window>
);
