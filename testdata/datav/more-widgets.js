// ===== gx-datav 第二批信息组件展示屏 (1920×1080) =====
//
// 运行: ./gox testdata/datav/more-widgets.js
// 给画廊当截图底本: KPI 指标卡 ×4 / 状态灯组 / 倒计时 / 时间轴 / 飞线 /
// 进度条组 / 滚动消息栏。
import { render, createSignal } from "gox";
import {
  BorderBox, TitleBar, KpiCard, StatusLight, Countdown, Timeline,
  FlightLines, ProgressList, MarqueeText,
  palette, seriesRandomWalk, makeRng,
} from "./index.js";

// 倒计时目标: 明天 20:00 (宿主页算 epoch 传进来; 这里用当前 +8h 演示)
const targetAt = Number(Temporal.Now.instant().epochMilliseconds) + 8 * 3600 * 1000 + 42 * 60 * 1000;

const [kpis, setKpis] = createSignal([
  { label: "今日 GMV", value: 1284560, delta: 12.4, trend: seriesRandomWalk(20, 100, 18, 3) },
  { label: "订单量", value: 38421, delta: 5.8, trend: seriesRandomWalk(20, 80, 20, 4) },
  { label: "客单价", value: 33.4, decimals: 1, delta: -2.1, trend: seriesRandomWalk(20, 60, 15, 5) },
  { label: "转化率", value: 4.86, decimals: 2, suffix: "", delta: 8.3, trend: seriesRandomWalk(20, 50, 22, 6) },
]);

const [services, setServices] = createSignal([
  { name: "API 网关", status: "ok", detail: "p99 38ms" },
  { name: "支付核心", status: "ok", detail: "p99 62ms" },
  { name: "搜索服务", status: "warn", detail: "延迟升高" },
  { name: "消息队列", status: "ok", detail: "堆积 0" },
  { name: "推荐引擎", status: "bad", detail: "实例 2/5 失联" },
  { name: "数据同步", status: "ok", detail: "滞后 1.2s" },
]);

const [events, setEvents] = createSignal([
  { time: "16:58", title: "支付通道延迟恢复", detail: "延迟从 210ms 回落至 38ms", status: "ok" },
  { time: "16:41", title: "推荐引擎实例失联", detail: "node-07 / node-09 心跳超时", status: "bad" },
  { time: "16:20", title: "搜索服务延迟升高", detail: "p99 超过 500ms 阈值", status: "warn" },
  { time: "15:55", title: "大促预热活动上线", detail: "双十一主会场 · 全量发布", status: "info" },
  { time: "15:30", title: "库存同步任务完成", detail: "12/12 分片 · 零差异", status: "ok" },
  { time: "14:02", title: "CDN 节点扩容", detail: "新增华南 3 节点", status: "info" },
]);

// 飞线: 一张"抽象地图"上的几条链路 (画布坐标即摆位)
const FLIGHT_W = 560, FLIGHT_H = 380;
const [flights, setFlights] = createSignal([
  { from: [80, 300], to: [300, 90], value: 128 },
  { from: [120, 330], to: [430, 150], value: 96 },
  { from: [300, 90], to: [490, 280], value: 64 },
  { from: [430, 150], to: [180, 180], value: 42 },
  { from: [80, 300], to: [490, 280], value: 31 },
]);
const FLIGHT_LABELS = [
  { x: 66, y: 318, text: "华南仓" },
  { x: 276, y: 66, text: "华东枢纽" },
  { x: 404, y: 168, text: "华北仓" },
  { x: 462, y: 300, text: "西北仓" },
  { x: 156, y: 196, text: "西南仓" },
];

const [progress, setProgress] = createSignal([
  { name: "消息投递", value: 92, max: 100 },
  { name: "索引构建", value: 67, max: 100 },
  { name: "画像回流", value: 45, max: 100 },
  { name: "报表产出", value: 88, max: 100 },
]);

render(
  <window title="gx-datav 信息组件" width={1920} height={1080} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={12} gap={10}>

      <row width="100%" height={34} alignItems="center">
        <TitleBar title="信息组件族" subtitle="DATAV WIDGETS · 2026-10" align="left" fontSize={16}/>
        <spacer flexGrow={1}/>
        <text font={11} color={palette.sub}>gx-datav · 第二批</text>
      </row>

      {/* KPI 卡行 */}
      <row width="100%" height={110} gap={10}>
        <KpiCard label={() => kpis()[0].label} value={() => kpis()[0].value}
          delta={() => kpis()[0].delta} trend={() => kpis()[0].trend} width="24%"/>
        <KpiCard label={() => kpis()[1].label} value={() => kpis()[1].value}
          delta={() => kpis()[1].delta} trend={() => kpis()[1].trend} width="24%"/>
        <KpiCard label={() => kpis()[2].label} value={() => kpis()[2].value}
          decimals={1} unit=" 元" delta={() => kpis()[2].delta} trend={() => kpis()[2].trend} width="24%"/>
        <KpiCard label={() => kpis()[3].label} value={() => kpis()[3].value}
          decimals={2} unit=" %" delta={() => kpis()[3].delta} trend={() => kpis()[3].trend} width="24%"/>
      </row>

      <row width="100%" flexGrow={1} gap={10}>
        {/* 左: 状态灯 + 倒计时 */}
        <column width={430} gap={10}>
          <BorderBox type={1} width="100%" height={240}>
            <TitleBar title="服务健康" align="left" fontSize={14}/>
            <row flexGrow={1} alignItems="center">
              <StatusLight width="94%" items={() => services()} rowHeight={26}/>
            </row>
          </BorderBox>
          <BorderBox type={4} width="100%" height={150}>
            <TitleBar title="大促倒计时" align="left" fontSize={14}/>
            <row flexGrow={1} alignItems="center" justifyContent="center" gap={16}>
              <Countdown target={targetAt} size={40}/>
              <column gap={4}>
                <text font={12} color={palette.sub}>距双十一开场</text>
                <text font={11} color={palette.sub}>目标: 今日 24:00</text>
              </column>
            </row>
          </BorderBox>
          <BorderBox type={2} width="100%" flexGrow={1}>
            <TitleBar title="任务进度" align="left" fontSize={14}/>
            <row flexGrow={1} alignItems="center">
              <ProgressList width="94%" rows={() => progress()} rowHeight={30}/>
            </row>
          </BorderBox>
        </column>

        {/* 中: 飞线 */}
        <column flexGrow={1}>
          <BorderBox type={5} width="100%" height="100%">
            <TitleBar title="仓间调拨" subtitle="FLIGHT LINES" align="left" fontSize={14}/>
            <row flexGrow={1} alignItems="center" justifyContent="center">
              <FlightLines width={FLIGHT_W} height={FLIGHT_H}
                lines={() => flights()} labels={FLIGHT_LABELS}/>
            </row>
          </BorderBox>
        </column>

        {/* 右: 时间轴 */}
        <column width={520}>
          <BorderBox type={6} width="100%" height="100%">
            <TitleBar title="事件流水" align="left" fontSize={14}/>
            <row flexGrow={1} alignItems="center">
              <Timeline width="94%" events={() => events()}/>
            </row>
          </BorderBox>
        </column>
      </row>

      {/* 底部跑马灯 */}
      <row width="100%" height={36}>
        <BorderBox type={2} width="100%" height="100%" padding={4}>
          <MarqueeText
            text="● 库存同步任务 12/12 完成 · 支付通道延迟恢复正常 · 推荐引擎扩容中 · 明日 02:00 例行维护窗口"
            speed={65} fontSize={13} accent={palette.warn}
          />
        </BorderBox>
      </row>

    </column>
  </window>
);
