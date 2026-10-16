// ===== gx-datav 时钟面板 =====
//
// 大屏右上角的标配: HH:MM:SS (冒号每秒半亮) + 日期 + 星期。运行时
// **没有 Date**, 时间全部走 Temporal.Now.zonedDateTimeISO() —— 这也是
// 本项目时钟类组件的唯一正解 (见 README 的内置对象清单)。
//
// props:
//   size     主字号 (缺省 30)
//   color    主色 (缺省主文本色)
//   accent   冒号/装饰色 (缺省强调色)
//   showDate 日期行 (缺省 true)
//   showWeek 星期 (缺省 true)
//   width    画布宽 (缺省 190)

import { h } from "gx/gfx";
import { createSignal } from "gx/solid";
import { textWidth, pad2 } from "./theme.js";
import { alpha } from "./draw.js";

export function ClockPanel(props) {
  const size = props.size || 30;
  const color = props.color || "#dbe8ff";
  const accent = props.accent || "#22e6ff";
  const showDate = props.showDate !== false;
  const showWeek = props.showWeek !== false;
  const width = props.width || 190;

  const WEEK = ["周一", "周二", "周三", "周四", "周五", "周六", "周日"];

  const [tick, setTick] = createSignal(nowParts());
  // 对齐到整秒再起表: setInterval(1000) 会随启动相位漂移, 大屏挂一小时
  // 能差出几百毫秒 —— 用 Temporal 拿当前毫秒数, 补一个差额再进循环。
  const ms = Number(Temporal.Now.instant().epochMilliseconds);
  setTimeout(() => {
    setTick(nowParts());
    setInterval(() => setTick(nowParts()), 1000);
  }, 1000 - (ms % 1000));

  const hh = () => pad2(tick().hour);
  const mm = () => pad2(tick().minute);
  const ss = () => pad2(tick().second);
  // 冒号半亮: 每秒的前 500ms 亮, 后 500ms 暗 —— 心跳感
  const colonOn = () => tick().millisecond < 500 || tick().millisecond === undefined;

  const rowH = size * 1.2;
  const dateH = showDate || showWeek ? 16 : 0;
  const height = rowH + dateH + 8;

  return h("canvas", {
    width: width,
    height: height,
    onDraw: (ctx) => {
      // 居中坐标以 ctx.width 为准: width prop 可能是百分比字符串,
      // 字符串参与减法和除法会得 NaN (时间会整体偏移出画布)。
      const width = ctx.width;
      const t = tick(); // 订阅 → 每秒重绘
      const colon = colonOn();
      const y = 4;
      // 主时间 HH:MM:SS
      const cw = textWidth("0", size);
      let x = (width - (cw * 6 + textWidth(":", size) * 2)) / 2;
      drawSeg(ctx, pad2(t.hour), x, y, size, color); x += cw * 2;
      drawSeg(ctx, ":", x, y, size, colon ? accent : alpha(accent, 0.25)); x += textWidth(":", size);
      drawSeg(ctx, pad2(t.minute), x, y, size, color); x += cw * 2;
      drawSeg(ctx, ":", x, y, size, colon ? accent : alpha(accent, 0.25)); x += textWidth(":", size);
      drawSeg(ctx, pad2(t.second), x, y, size, color);

      // 日期 + 星期
      if (dateH) {
        const parts = [];
        if (showDate) parts.push(t.year + "-" + pad2(t.month) + "-" + pad2(t.day));
        if (showWeek) parts.push(WEEK[(t.dayOfWeek + 6) % 7]);
        const line = parts.join("  ");
        const fs = 12;
        ctx.drawText(line, (width - textWidth(line, fs)) / 2, y + size * 1.15, fs, "#7e93bd");
      }
    },
  });
}

function drawSeg(ctx, txt, x, y, size, color) {
  ctx.drawText(txt, x, y, size, color);
}

function nowParts() {
  const z = Temporal.Now.zonedDateTimeISO();
  return {
    hour: z.hour, minute: z.minute, second: z.second,
    year: z.year, month: z.month, day: z.day,
    dayOfWeek: z.dayOfWeek, millisecond: Number(z.millisecond === undefined ? 0 : z.millisecond),
  };
}
