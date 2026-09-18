// P2-9 演示: <image> 图片显示。
// 运行: go run . testdata/image_demo.js   （**必须在仓库根目录运行**）
// 现象: 同一张 32x32 四象限图五态对照 ——
//   1. 不给尺寸      按自然尺寸显示 (32x32);
//   2. 放大 96x96    最近邻采样, 像素块边界清晰 (看中间那道白十字被拉宽成色块);
//   3. 缩小 16x16    缩到原图一半;
//   4. 坏路径        灰底 + 45° 交叉线占位, stderr 打印一次警告, 不中断其它内容;
//   5. disabled      罩一层半透明灰。
//
// 两个容易踩的点:
//   - `src` 的相对路径按**进程工作目录**解释, 不是"脚本所在目录" (脚本可以从
//     stdin / 字符串 / 打包产物来, 没有所在目录这个概念)。
//   - 不给 width/height 时用图片自然尺寸; 加载失败则给 16x16 兜底尺寸 ——
//     兜底必须非 0, 否则 0 尺寸子树会被整支跳过, 界面上什么都看不到。
import { h, window, render } from "gx/gfx";

const SRC = "testdata/image_demo.png";

render(
  <column gap={10} padding={12}>
    <text font={13} color="#8a8a8a">natural (no size given)</text>
    <image src={SRC} />

    <text font={13} color="#8a8a8a">scaled up 96x96 (nearest)</text>
    <image src={SRC} width={96} height={96} />

    <text font={13} color="#8a8a8a">scaled down 16x16</text>
    <image src={SRC} width={16} height={16} />

    <text font={13} color="#8a8a8a">missing file -&gt; placeholder</text>
    <image src="testdata/definitely_missing.png" width={96} height={48} />

    <text font={13} color="#8a8a8a">disabled</text>
    <image src={SRC} width={64} height={64} disabled />
  </column>,
  window({ title: "Image demo", width: 220, height: 460 })
);
