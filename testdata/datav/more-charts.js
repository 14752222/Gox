// ===== gx-datav 第二批图表组件展示屏 (1920×1080) =====
//
// 运行: ./gox testdata/datav/more-charts.js
// 给画廊当截图底本: 水球组 / 玫瑰 / 漏斗 / 子弹 / 散点 / 热力 / 组合 / 词云 /
// 进度条组, 全部确定性种子 (截图可复现)。
import { render, createSignal } from "gox";
import {
  BorderBox, TitleBar, LiquidFill, RoseChart, FunnelChart, BulletChart,
  ScatterChart, HeatMap, ComboChart, WordCloud, ProgressList, MarqueeText,
  palette, seriesRandomWalk, makeRng,
} from "./index.js";

const [liquid, setLiquid] = createSignal([86, 62, 41]);
const [rose, setRose] = createSignal([
  { name: "搜索", value: 4520 },
  { name: "直访", value: 3120 },
  { name: "广告", value: 2180 },
  { name: "社交", value: 1640 },
  { name: "邮件", value: 890 },
]);
const [funnel, setFunnel] = createSignal([
  { name: "曝光", value: 128400 },
  { name: "点击", value: 38200 },
  { name: "加购", value: 12400 },
  { name: "下单", value: 5180 },
  { name: "支付", value: 2960 },
]);
const [bullet, setBullet] = createSignal([
  { name: "订单量", value: 86, target: 75, max: 100 },
  { name: "退单率", value: 12, target: 15, max: 100, unit: "%" },
  { name: "履约时效", value: 94, target: 90, max: 100, unit: "%" },
  { name: "客服满意度", value: 88, target: 92, max: 100, unit: "%" },
]);
const [scatter, setScatter] = createSignal([
  { name: "一线", data: makeScatter(14, 70, 55, 21) },
  { name: "新一线", data: makeScatter(12, 45, 40, 22) },
]);
const [comboBars, setComboBars] = createSignal([{ name: "销量", data: seriesRandomWalk(12, 60, 22, 31) }]);
const [comboLines, setComboLines] = createSignal([{ name: "转化率", data: seriesRandomWalk(12, 42, 9, 32) }]);
const [progress, setProgress] = createSignal([
  { name: "存储", value: 78, max: 100 },
  { name: "计算", value: 46, max: 100 },
  { name: "带宽", value: 91, max: 100 },
  { name: "连接数", value: 33, max: 100 },
]);

const HOURS = ["00", "03", "06", "09", "12", "15", "18", "21", "24"];
const DAYS = ["周一", "周二", "周三", "周四", "周五", "周六", "周日"];
const [heat, setHeat] = createSignal(makeHeat());

const [words, setWords] = createSignal([
  { text: "转化率", weight: 100 }, { text: "留存", weight: 82 },
  { text: "GMV", weight: 76 }, { text: "复购", weight: 64 },
  { text: "拉新", weight: 58 }, { text: "客单价", weight: 52 },
  { text: "ROI", weight: 47 }, { text: "活跃", weight: 41 },
  { text: "漏斗", weight: 36 }, { text: "标签", weight: 31 },
  { text: "分群", weight: 27 }, { text: "归因", weight: 23 },
  { text: "埋点", weight: 19 }, { text: "看板", weight: 15 },
  { text: "预警", weight: 12 },
]);

// 确定性模拟数据
function makeScatter(n, cx, cy, seed) {
  const rng = makeRng(seed);
  const out = [];
  for (let i = 0; i < n; i++) {
    out.push({
      x: Math.round(cx + (rng() - 0.5) * 60),
      y: Math.round(cy + (rng() - 0.5) * 50),
      r: 4 + Math.round(rng() * 20),
    });
  }
  return out;
}
function makeHeat() {
  const rng = makeRng(77);
  const out = [];
  for (let d = 0; d < 7; d++) {
    const row = [];
    for (let h = 0; h < 24; h++) {
      // 白天高晚上低的双峰形状
      const base = 40 * Math.exp(-Math.pow((h - 11) / 4.5, 2)) + 55 * Math.exp(-Math.pow((h - 20) / 3, 2));
      row.push(Math.round(Math.max(2, base * (0.55 + rng() * 0.7))));
    }
    out.push(row);
  }
  return out;
}

render(
  <window title="gx-datav 图表组件" width={1920} height={1080} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={12} gap={10}>

      <row width="100%" height={34} alignItems="center">
        <TitleBar title="图表组件族" subtitle="DATAV CHARTS · 2026-10" align="left" fontSize={16}/>
        <spacer flexGrow={1}/>
        <text font={11} color={palette.sub}>gx-datav · 第二批</text>
      </row>

      <row width="100%" flexGrow={1} gap={10}>

        {/* ---- 左列 ---- */}
        <column width={430} gap={10}>
          <BorderBox type={1} width="100%" height={250}>
            <TitleBar title="资源水位" align="left" fontSize={14}/>
            <row flexGrow={1} justifyContent="space-around" alignItems="center">
              <LiquidFill value={() => liquid()[0]} size={130} label="存储"/>
              <LiquidFill value={() => liquid()[1]} size={130} color={palette.warn} waveColor={palette.warn} label="计算"/>
              <LiquidFill value={() => liquid()[2]} size={130} color={palette.bad} waveColor={palette.bad} label="告警"/>
            </row>
          </BorderBox>
          <BorderBox type={4} width="100%" flexGrow={1}>
            <TitleBar title="搜索热词" align="left" fontSize={14}/>
            <row flexGrow={1} alignItems="center" justifyContent="center">
              <WordCloud width="94%" height={220} words={() => words()}/>
            </row>
          </BorderBox>
        </column>

        {/* ---- 中列 ---- */}
        <column flexGrow={1} gap={10}>
          <BorderBox type={2} width="100%" flexGrow={1}>
            <TitleBar title="销量 × 转化率" subtitle="双 Y 轴" align="left" fontSize={14}/>
            <row flexGrow={1} alignItems="center">
              <ComboChart
                width="100%" height={330}
                bars={() => comboBars()}
                lines={() => comboLines()}
                labels={HOURS}
              />
            </row>
          </BorderBox>
          <BorderBox type={1} width="100%" height={300}>
            <TitleBar title="玫瑰 × 漏斗" align="left" fontSize={14}/>
            <row flexGrow={1} alignItems="center" gap={10}>
              <RoseChart size={250} data={() => rose()}/>
              <FunnelChart width={400} data={() => funnel()}/>
            </row>
          </BorderBox>
        </column>

        {/* ---- 右列 ---- */}
        <column width={470} gap={10}>
          <BorderBox type={5} width="100%" height={250}>
            <TitleBar title="一周访问热力" subtitle="7 × 24" align="left" fontSize={14}/>
            <row flexGrow={1} alignItems="center">
              <HeatMap width="100%" height={190} cellH={22}
                data={() => heat()} xLabels={HOURS} yLabels={DAYS}/>
            </row>
          </BorderBox>
          <BorderBox type={3} width="100%" height={240}>
            <TitleBar title="城市象限" subtitle="气泡 = 门店数" align="left" fontSize={14}/>
            <row flexGrow={1} alignItems="center">
              <ScatterChart width="100%" height={180} series={() => scatter()}
                xLabel="GMV (千万)" yLabel="复购率" sizeRange={[6, 24]}/>
            </row>
          </BorderBox>
          <BorderBox type={6} width="100%" flexGrow={1}>
            <TitleBar title="目标达成" align="left" fontSize={14}/>
            <row flexGrow={1} alignItems="center" gap={16}>
              <BulletChart width="56%" rows={() => bullet()}/>
              <column flexGrow={1} gap={8}>
                <TitleBar title="系统资源" align="left" fontSize={12}/>
                <ProgressList width="100%" rows={() => progress()} rowHeight={28}/>
              </column>
            </row>
          </BorderBox>
        </column>
      </row>

      {/* 底部: 跑马灯 */}
      <row width="100%" height={36}>
        <BorderBox type={2} width="100%" height="100%" padding={4}>
          <MarqueeText
            text="● 双十一预热进行中 · 库存同步任务 12/12 完成 · 支付通道延迟 38ms 恢复正常 · 明日 02:00 例行维护窗口"
            speed={70} fontSize={13} accent={palette.warn}
          />
        </BorderBox>
      </row>

    </column>
  </window>
);
