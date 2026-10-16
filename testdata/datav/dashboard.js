// ===== gx-datav 完整大屏示例 (1920×1080 设计稿) =====
//
// 运行: ./gox testdata/datav/dashboard.js
// 现象: 深空底大屏, 三段式布局 (顶栏 / 三列主体 / 底部通栏), 全部
//       15 个组件齐上场; 数据每 2 秒模拟刷新一次, 数字/条/指针/表格
//       各自缓动, 表格持续轮播。
//
// 布局为什么这样排:
//   顶栏   80px  —— 标题居中最重 (大屏的"脸"), 时钟右上, 两个 KPI 左上
//   主体   flex row 三列 —— 左 460 / 中 flex / 右 460, 经典"两翼包中间"
//           中间列上方是主折线 (视觉重心), 下方柱状 + 中部 KPI 翻牌
//   底部   150px —— 通栏轮播表格, 压住整屏重心
// 全部尺寸按 1920×1080 设计; 上真屏 (LED/投影) 时窗口按目标分辨率开,
// 组件内部坐标随画布走, 不需要改代码 (见 README「多屏适配」)。

import { h, render, createSignal } from "gox";
import {
  BorderBox, TitleBar, FlipBoard, CountUp, RingProgress, Gauge,
  LineChart, BarChart, PieChart, RadarChart, RankingList, ScrollTable, ClockPanel,
  palette, seriesColors, seriesRandomWalk, fmtInt,
} from "./index.js";

// ---- 模拟数据源 (确定性种子 → 每次运行同一组数, 截图可复现) ----
const [kpi, setKpi] = createSignal([
  { label: "今日订单", value: 128456, prefix: "" },
  { label: "在线用户", value: 8624 },
  { label: "交易额", value: 3842179, prefix: "¥" },
  { label: "转化率", value: 68.4, suffix: "%", decimals: 1 },
]);
const [ringVals, setRingVals] = createSignal([78, 92, 64]);
const [gaugeVal, setGaugeVal] = createSignal(72);
const [lineData, setLineData] = createSignal([
  { name: "订单量", data: seriesRandomWalk(24, 120, 18, 7) },
  { name: "退单量", data: seriesRandomWalk(24, 30, 8, 11) },
]);
const [barData, setBarData] = createSignal(seriesRandomWalk(12, 60, 22, 5, 10, 120));
const [pieData, setPieData] = createSignal([
  { name: "移动端", value: 4520, color: seriesColors[0] },
  { name: "PC 端", value: 3120, color: seriesColors[1] },
  { name: "小程序", value: 2180, color: seriesColors[2] },
  { name: "第三方", value: 980, color: seriesColors[3] },
]);
const [radarData, setRadarData] = createSignal([
  { name: "本期", values: [82, 90, 68, 76, 88, 72] },
  { name: "上期", values: [64, 72, 80, 58, 66, 82] },
]);
const [rankRows, setRankRows] = createSignal([
  { name: "华南大区", value: 9821 },
  { name: "华东大区", value: 8764 },
  { name: "华北大区", value: 7932 },
  { name: "西南大区", value: 6418 },
  { name: "西北大区", value: 5210 },
  { name: "东北大区", value: 4387 },
]);
const [tableRows, setTableRows] = createSignal([
  { id: "A-10241", region: "华南", ch: "线上商城", amt: 128900, st: "已完成" },
  { id: "A-10242", region: "华东", ch: "APP 推送", amt: 56200, st: "进行中" },
  { id: "A-10243", region: "华北", ch: "线下门店", amt: 93400, st: "已完成" },
  { id: "A-10244", region: "西南", ch: "第三方", amt: 27800, st: "待付款" },
  { id: "A-10245", region: "西北", ch: "线上商城", amt: 76100, st: "已完成" },
  { id: "A-10246", region: "东北", ch: "小程序", amt: 41500, st: "进行中" },
  { id: "A-10247", region: "华南", ch: "APP 推送", amt: 112300, st: "已完成" },
  { id: "A-10248", region: "华东", ch: "线下门店", amt: 68900, st: "待付款" },
]);

// 每 2s 刷新一批 (只动变化的量, 其余保持 —— 避免整屏乱颤)
setInterval(() => {
  setKpi((prev) => prev.map((k) => {
    const drift = k.decimals ? (Math.random() - 0.5) * 4 : Math.round((Math.random() - 0.45) * k.value * 0.03);
    return { ...k, value: Math.max(0, k.value + drift) };
  }));
  setRingVals([rnd(60, 99), rnd(70, 99), rnd(40, 95)]);
  setGaugeVal(rnd(55, 92));
  setLineData([
    { name: "订单量", data: seriesRandomWalk(24, 120, 18, Math.floor(Math.random() * 1e6)) },
    { name: "退单量", data: seriesRandomWalk(24, 30, 8, Math.floor(Math.random() * 1e6)) },
  ]);
  setBarData(seriesRandomWalk(12, 60, 22, Math.floor(Math.random() * 1e6), 10, 120));
  setRankRows((prev) => {
    const next = prev.map((r) => ({ ...r, value: Math.max(100, r.value + Math.round((Math.random() - 0.5) * 900)) }));
    next.sort((a, b) => b.value - a.value);
    return next;
  });
}, 2000);

function rnd(lo, hi) { return Math.round(lo + Math.random() * (hi - lo)); }

const HOURS = Array.from({ length: 24 }, (_, i) => (i < 10 ? "0" + i : String(i)) + ":00");
const REGIONS = ["0h", "2h", "4h", "6h", "8h", "10h", "12h", "14h", "16h", "18h", "20h", "22h"];

render(
  <window title="gx-datav 数据大屏" width={1920} height={1080} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} gap={10} padding={12}>

      {/* ===== 顶栏 ===== */}
      <row width="100%" height={86} alignItems="center">
        <column width={520} gap={6}>
          <TitleBar title="运营数据中心" subtitle="REAL-TIME OPERATIONS" fontSize={22} align="left"/>
          <row gap={18}>
            <CountUp value={() => kpi()[0].value} fontSize={20} color={palette.accent}/>
            <column gap={2}>
              <text font={11} color={palette.sub}>今日订单 (实时)</text>
              <text font={11} color={palette.sub}>较昨日 +12.4%</text>
            </column>
          </row>
        </column>
        <spacer flexGrow={1}/>
        <column alignItems="center" gap={4} width={340}>
          <TitleBar title="Gox 数据可视化大屏" align="center" fontSize={26}/>
          <text font={11} color={palette.sub}>DATA VISUALIZATION DASHBOARD · 1920×1080</text>
        </column>
        <spacer flexGrow={1}/>
        <column width={520} alignItems="end" gap={6}>
          <ClockPanel size={26}/>
          <row gap={16}>
            <column alignItems="end" gap={2}>
              <text font={11} color={palette.sub}>转化率</text>
              <CountUp value={() => kpi()[3].value} fontSize={18} decimals={1} suffix="%" color={palette.ok}/>
            </column>
            <column alignItems="end" gap={2}>
              <text font={11} color={palette.sub}>在线用户</text>
              <CountUp value={() => kpi()[1].value} fontSize={18} color={palette.ok}/>
            </column>
          </row>
        </column>
      </row>

      {/* ===== 主体三列 ===== */}
      <row width="100%" flexGrow={1} gap={10}>

        {/* ---- 左列 ---- */}
        <column width={460} gap={10}>
          <BorderBox type={1} width="100%" height={252}>
            <TitleBar title="核心指标" align="left" fontSize={14}/>
            <row flexGrow={1} justifyContent="space-around" alignItems="center">
              <RingProgress value={() => ringVals()[0]} size={110} label="订单" sub="完成率"/>
              <RingProgress value={() => ringVals()[1]} size={110} color={palette.ok} label="履约" sub="及时率"/>
              <RingProgress value={() => ringVals()[2]} size={110} color={palette.warn} label="库存" sub="健康度"/>
            </row>
          </BorderBox>
          <BorderBox type={4} width="100%" flexGrow={1}>
            <TitleBar title="大区销售榜" align="left" fontSize={14}/>
            <RankingList width="100%" rows={() => rankRows()} rowHeight={38}/>
          </BorderBox>
        </column>

        {/* ---- 中列 ---- */}
        <column flexGrow={1} gap={10}>
          <BorderBox type={5} width="100%" flexGrow={1}>
            <TitleBar title="24 小时订单趋势" subtitle="单位: 单" align="left" fontSize={14}/>
            <LineChart
              width="100%" height={330}
              series={() => lineData()}
              labels={HOURS}
              yMax={220}
            />
          </BorderBox>
          <row width="100%" height={240} gap={10}>
            <BorderBox type={2} width="58%" height="100%">
              <TitleBar title="时段销量" align="left" fontSize={14}/>
              <BarChart width="100%" height={190} data={() => barData()} labels={REGIONS}/>
            </BorderBox>
            <BorderBox type={3} width="42%" height="100%">
              <TitleBar title="实时交易额" align="left" fontSize={14}/>
              <row flexGrow={1} alignItems="center" justifyContent="center" gap={24}>
                <FlipBoard value={() => kpi()[2].value} digits={8} size={26} accent={palette.warn}/>
                <column gap={4}>
                  <text font={11} color={palette.sub}>累计交易额 (元)</text>
                  <text font={11} color={palette.sub}>较昨日 +8.2%</text>
                </column>
              </row>
            </BorderBox>
          </row>
        </column>

        {/* ---- 右列 ---- */}
        <column width={460} gap={10}>
          <BorderBox type={6} width="100%" height={240}>
            <TitleBar title="系统负载" align="left" fontSize={14}/>
            <row flexGrow={1} alignItems="center" justifyContent="center">
              <Gauge value={() => gaugeVal()} min={0} max={100} unit="%" label="综合负载" size={300}/>
            </row>
          </BorderBox>
          <BorderBox type={1} width="100%" height={252}>
            <TitleBar title="渠道占比" align="left" fontSize={14}/>
            <row flexGrow={1} alignItems="center">
              <PieChart
                donut={true} size={190}
                data={() => pieData()}
                center="8,080" unit=" 单"
              />
              {/* 图例: 手工一列 (色块 + 名称 + 值), 比组件内嵌图例更灵活 */}
              <column gap={10} flexGrow={1} padding={10}>
                <text font={12} color={palette.sub}>渠道构成</text>
                <column gap={6} each={() => pieData()} key={(d) => d.name}>
                  {(d) => (
                    <row gap={8} alignItems="center">
                      <rect width={10} height={10} radius={2} background={() => d.color}/>
                      <text font={12} color={palette.text}>{() => d.name}</text>
                      <spacer flexGrow={1}/>
                      <text font={12} color={palette.accent}>{() => fmtInt(d.value)}</text>
                    </row>
                  )}
                </column>
              </column>
            </row>
          </BorderBox>
          <BorderBox type={4} width="100%" flexGrow={1}>
            <TitleBar title="能力评估" align="left" fontSize={14}/>
            <row flexGrow={1} alignItems="center" justifyContent="center">
              <RadarChart
                size={230}
                axes={["订单", "履约", "库存", "售后", "营销", "风控"]}
                series={() => radarData()}
              />
            </row>
          </BorderBox>
        </column>
      </row>

      {/* ===== 底部通栏 ===== */}
      <BorderBox type={2} width="100%" height={150}>
        <row width="100%" flexGrow={1} gap={16}>
          <column width={220} gap={6}>
            <TitleBar title="实时流水" align="left" fontSize={14}/>
            <text font={11} color={palette.sub}>最新 8 笔订单滚动播报</text>
            <spacer flexGrow={1}/>
            <CountUp value={() => kpi()[1].value} fontSize={18} suffix=" 人在线" color={palette.accent}/>
          </column>
          <ScrollTable
            width="100%" height={104} rowHeight={26}
            columns={[
              { key: "id", label: "订单号", width: 1.2 },
              { key: "region", label: "大区", width: 0.8 },
              { key: "ch", label: "渠道", width: 1 },
              { key: "amt", label: "金额 (元)", width: 1, align: "right" },
              { key: "st", label: "状态", width: 0.8, align: "right" },
            ]}
            rows={() => tableRows()}
          />
        </row>
      </BorderBox>

    </column>
  </window>
);
