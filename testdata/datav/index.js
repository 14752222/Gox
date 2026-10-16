// ===== gx-datav 统一导出 =====
//
// 应用侧一行引入全家桶:
//   import { BorderBox, TitleBar, FlipBoard, LineChart, ... } from "../datav/index.js";
// 库内部仍按文件细分导入 (避免循环依赖)。
//
// 导出的常量 (palette / seriesColors / fontSizes) 是主题契约: demo 与
// 自定义组件都应该用它们取色, 而不是各写一份 hex —— 换肤只改 theme.js。

export {
  palette, seriesColors, fontSizes,
  clamp, lerp, easeOutCubic, easeOutBack, deg2rad, rad2deg, polar,
  pad2, fmtInt, fmtCompact, trimFloat, niceCeil,
  textWidth, truncateTo, makeRng, seriesRandomWalk, normalize,
} from "./theme.js";

export {
  smoothPath, areaFill, vGradient, glowCircle, dashedLine, grid,
  roundBar, centerText, rightText, tickLabels, polyPath,
  alpha, mix,
} from "./draw.js";

export { BorderBox } from "./border.js";
export { TitleBar } from "./title.js";
export { FlipBoard, CountUp } from "./flip.js";
export { RingProgress } from "./ring.js";
export { Gauge } from "./gauge.js";
export { LineChart } from "./line.js";
export { BarChart } from "./bar.js";
export { ComboChart } from "./combo.js";
export { PieChart } from "./pie.js";
export { RoseChart } from "./rose.js";
export { RadarChart } from "./radar.js";
export { ScatterChart } from "./scatter.js";
export { HeatMap } from "./heatmap.js";
export { FunnelChart } from "./funnel.js";
export { RankingList } from "./ranking.js";
export { ProgressList } from "./progress.js";
export { BulletChart } from "./bullet.js";
export { ScrollTable } from "./table.js";
export { ClockPanel } from "./clock.js";
export { Countdown } from "./countdown.js";
export { LiquidFill } from "./liquid.js";
export { StatusLight } from "./status.js";
export { Sparkline } from "./sparkline.js";
export { KpiCard } from "./kpicard.js";
export { MarqueeText } from "./marquee.js";
export { Timeline } from "./timeline.js";
export { FlightLines } from "./flight.js";
export { WordCloud } from "./wordcloud.js";

// 组件清单 (文档/画廊/测试共用一份真相)
export const components = {
  BorderBox: "科技风面板边框 (6 型)",
  TitleBar: "装饰标题",
  FlipBoard: "数字翻牌器",
  CountUp: "数字缓动",
  RingProgress: "环形进度",
  Gauge: "仪表盘",
  LineChart: "折线图",
  BarChart: "柱状图",
  ComboChart: "柱线组合图 (双轴)",
  PieChart: "饼图 / 环图",
  RoseChart: "玫瑰图 (南丁格尔)",
  RadarChart: "雷达图",
  ScatterChart: "气泡散点图",
  HeatMap: "热力图",
  FunnelChart: "漏斗图",
  RankingList: "排行榜",
  ProgressList: "进度条组",
  BulletChart: "子弹图",
  ScrollTable: "轮播表格",
  ClockPanel: "时钟面板",
  Countdown: "倒计时",
  LiquidFill: "水球图",
  StatusLight: "状态指示灯组",
  Sparkline: "迷你趋势线",
  KpiCard: "KPI 指标卡",
  MarqueeText: "滚动消息栏",
  Timeline: "时间轴",
  FlightLines: "飞线图",
  WordCloud: "词云",
};
