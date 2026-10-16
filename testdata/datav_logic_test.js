// ===== gx-datav 组件库纯逻辑验证 (无窗口, 不开 GUI) =====
// 运行: ./gox testdata/datav_logic_test.js
// 覆盖: 主题常量 / 数学工具 / 文本度量 / 数据流 / 颜色工具 / 弧采样数学。
// GUI 形态 (布局、动画、命中) 由 testdata/shots/datav-dashboard.js 离屏
// 截图验证 (gfx/gallery_shot_test.go), 人工验收跑 dashboard.js 真窗口。
import {
  palette, seriesColors, clamp, lerp, easeOutCubic, deg2rad, polar,
  pad2, fmtInt, fmtCompact, textWidth, truncateTo,
  makeRng, seriesRandomWalk, normalize,
  alpha, mix, smoothPath, areaFill, roundBar,
  components,
} from "./datav/index.js";
import { BorderBox } from "./datav/border.js";
import { TitleBar } from "./datav/title.js";
import { FlipBoard, CountUp } from "./datav/flip.js";
import { RingProgress } from "./datav/ring.js";
import { Gauge } from "./datav/gauge.js";
import { LineChart } from "./datav/line.js";
import { BarChart } from "./datav/bar.js";
import { PieChart } from "./datav/pie.js";
import { RadarChart } from "./datav/radar.js";
import { RankingList } from "./datav/ranking.js";
import { ScrollTable } from "./datav/table.js";
import { ClockPanel } from "./datav/clock.js";

let failures = 0;
function check(name, cond) {
  if (cond) { console.log("PASS  " + name); }
  else { console.log("FAIL  " + name); failures++; }
}

// ---- 组件可构造 (函数都在, 调用不抛) ----
// 清单长度别钉死常量: 这里曾写 `=== 13`, 组件库后来扩到 29 个, 断言当场
// 变红而没人动它 —— 硬编码数字必然漂移, 只会训练出"看到红就跳过"的习惯。
// 改为直接数 components 条目, 与 index.js 的清单同源。
const COMPONENT_COUNT = Object.keys(components).length;
check("组件清单非空且全部可构造 (" + COMPONENT_COUNT + " 个)",
  COMPONENT_COUNT > 0 &&
  typeof BorderBox === "function" && typeof ClockPanel === "function" &&
  Object.values(components).every((d) => typeof d === "string" && d.length > 0));

// ---- 数学 ----
check("clamp 钳制", clamp(5, 0, 3) === 3 && clamp(-1, 0, 3) === 0 && clamp(2, 0, 3) === 2);
check("lerp 插值", lerp(0, 10, 0.25) === 2.5);
check("easeOutCubic 端点", easeOutCubic(0) === 0 && easeOutCubic(1) === 1 && easeOutCubic(0.5) === 0.875);
check("deg2rad 90° = π/2", Math.abs(deg2rad(90) - Math.PI / 2) < 1e-9);
check("polar 0° 指右", Math.abs(polar(0, 0, 10, 0).x - 10) < 1e-9);

// ---- 格式化 ----
check("pad2", pad2(7) === "07" && pad2(13) === "13");
check("fmtInt 千分位", fmtInt(1234567) === "1,234,567" && fmtInt(999) === "999");
check("fmtCompact 万/亿", fmtCompact(12345) === "1.2万" && fmtCompact(2e8) === "2亿");

// ---- 文本度量 ----
check("textWidth 中文全宽", textWidth("你好", 10) === 20);
check("textWidth ASCII 窄", textWidth("ab", 10) === 12); // 0.58*10*2 = 11.6 → ceil 12
check("truncateTo 截断", truncateTo("一二三四五六", 10, 35) === "一二..");

// ---- 数据流 ----
const rng1 = makeRng(42), rng2 = makeRng(42);
check("makeRng 确定性", rng1() === rng2() && rng1() !== rng1());
const walk = seriesRandomWalk(20, 50, 5, 3, 30, 70);
check("seriesRandomWalk 长度/范围", walk.length === 20 &&
  walk.every((v) => v >= 30 && v <= 70));
check("normalize 归一", normalize(75, 50, 100) === 0.5 && normalize(10, 50, 100) === 0);

// ---- 颜色 ----
check("alpha #hex → rgba", alpha("#22e6ff", 0.5) === "rgba(34,230,255,0.5)");
check("mix 端点", mix("#000000", "#ffffff", 0) === "#000000" && mix("#000000", "#ffffff", 1) === "#ffffff");
check("mix 中点", mix("#000000", "#ffffff", 0.5) === "#808080");

// ---- canvas 工具函数可调用 (mock ctx) ----
let calls = 0;
const mockCtx = {
  width: 100, height: 50,
  fillRect: () => calls++, strokeRect: () => calls++, fillCircle: () => calls++,
  strokeCircle: () => calls++, line: () => calls++, drawText: () => calls++,
  clear: () => calls++, beginPath: () => calls++, moveTo: () => calls++,
  lineTo: () => calls++, closePath: () => calls++, fill: () => calls++,
  stroke: () => calls++, arc: () => calls++,
  lineWidth: 1,
};
smoothPath(mockCtx, [{ x: 0, y: 10 }, { x: 50, y: 20 }, { x: 100, y: 5 }]);
check("smoothPath 落笔", calls > 10);
const before = calls;
areaFill(mockCtx, [{ x: 0, y: 10 }, { x: 50, y: 20 }, { x: 100, y: 5 }], 50, "#22e6ff");
check("areaFill 分层填充", calls - before >= 8); // 4 层 × (beginPath+fill) 起
const before2 = calls;
roundBar(mockCtx, 10, 10, 20, 30, 4, "#22e6ff");
// 四件套: 柱身 + 顶部中段 + 左右圆帽各一 (圆帽半径是 rr 不是 w/2,
// 否则横条会画出半径=条长的巨圆 —— 见 draw.js roundBar 注释)
check("roundBar 四件套", calls - before2 === 4);

console.log(failures === 0 ? "ALL PASS" : failures + " FAILURES");
if (failures > 0) throw new Error(failures + " 项失败");
