// ===== gx-datav 装饰标题 =====
//
// 大屏面板的标题不是"一段文字", 而是面板的眉: 要有标记 (竖条/菱形)、
// 要有翼线、主副标题分层。两种对齐:
//
//   left    竖条 + 主标题 (+ 副标题) + 右侧延伸翼线    (列表型面板)
//   center  左右翼线 + 菱形 + 主标题                   (居中大面板)
//
// 高度自适应字号, 不给固定高度 —— 嵌在 BorderBox 里当第一行正好。
// 装饰全部画在一个铺满的 canvas 上, 文本走 canvas drawText (发光色
// 直接用强调色即可, 内核没有 shadowBlur, 不假装有)。

import { h } from "gx/gfx";
import { palette, textWidth } from "./theme.js";
import { alpha } from "./draw.js";

export function TitleBar(props) {
  const title = props.title || "";
  const subtitle = props.subtitle || "";
  const color = props.color || palette.accent;
  const textColor = props.textColor || palette.text;
  const subColor = props.subColor || palette.sub;
  const align = props.align || "left";
  const size = props.fontSize || 16;
  const subSize = props.subFontSize || 11;
  const deco = props.deco !== false;

  const hasSub = subtitle !== "";
  const height = (hasSub ? subSize + 6 : 0) + size + 10;

  return h("column", { width: "100%" },
    h("canvas", {
      width: "100%",
      height: height,
      onDraw: (ctx) => {
        const w = ctx.width, hh = ctx.height;
        const cy = Math.round(size / 2) + 2;
        if (align === "center") {
          // 居中: 左右翼线 + 菱形 + 标题
          const tw = textWidth(title, size);
          const cx = w / 2;
          if (deco) {
            ctx.fillRect(cx - tw / 2 - 22, cy + size / 2 - 4, 5, 5, color); // 菱形 (旋转方形的近似: 画小方块+两侧斜线)
            ctx.line(cx - tw / 2 - 26, cy + size / 2 - 1.5, cx - tw / 2 - 18, cy + size / 2 - 1.5, color);
            ctx.line(cx - tw / 2 - 17, cy + size / 2 - 4, cx - tw / 2 - 17, cy + size / 2 + 1, color);
            ctx.line(cx + tw / 2 + 22, cy + size / 2 - 4, cx + tw / 2 + 17, cy + size / 2 - 4, color);
            ctx.line(cx + tw / 2 + 17, cy + size / 2 - 4, cx + tw / 2 + 17, cy + size / 2 + 1, color);
            ctx.line(cx + tw / 2 + 26, cy + size / 2 - 1.5, cx + tw / 2 + 18, cy + size / 2 - 1.5, color);
            // 翼线: 菱形外侧各一道, 越长越有"眉"的感觉
            ctx.line(cx - tw / 2 - 40, cy + size / 2 - 1.5, cx - tw / 2 - 30, cy + size / 2 - 1.5, alpha(color, 0.6));
            ctx.line(cx + tw / 2 + 30, cy + size / 2 - 1.5, cx + tw / 2 + 40, cy + size / 2 - 1.5, alpha(color, 0.6));
          }
          ctx.drawText(title, cx - tw / 2, cy - size / 2 + 2, size, textColor);
          if (hasSub) {
            const sw = textWidth(subtitle, subSize);
            ctx.drawText(subtitle, cx - sw / 2, cy + size / 2 + 6, subSize, subColor);
          }
        } else {
          // 左对齐: 竖条 + 标题 (+ 副标题) + 右侧翼线
          if (deco) {
            ctx.fillRect(0, cy - 1, 3, size + 2, color);
            ctx.fillRect(6, cy - 1, 1, size + 2, alpha(color, 0.45));
          }
          const tx = deco ? 12 : 0;
          ctx.drawText(title, tx, cy - size / 2 + 2, size, textColor);
          if (hasSub) {
            const sx = tx + textWidth(title, size) + 10;
            ctx.drawText(subtitle, sx, cy - subSize / 2 + 1, subSize, subColor);
          }
          // 右侧翼线: 从标题尾到面板右缘
          if (deco) {
            const lineStart = tx + textWidth(title, size) + (hasSub ? textWidth(subtitle, subSize) + 16 : 14);
            if (lineStart < w - 20) {
              ctx.line(lineStart, cy + size / 2 - 1.5, w, cy + size / 2 - 1.5, alpha(color, 0.35));
              // 翼线末端的小箭头块
              ctx.fillRect(w - 4, cy + size / 2 - 3.5, 4, 5, alpha(color, 0.8));
            }
          }
        }
      },
    })
  );
}
