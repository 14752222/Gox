// P2-6 演示: text 的自动换行与省略号。
// 运行: go run . testdata/multiline_demo.js
// 现象: 同一段中英混排文本三态对照 ——
//   1. `wrap`           按 260px 宽度自动折行, 高度跟着行数长;
//   2. `wrap + ellipsis` 只留 2 行, 末行截断并补 "...";
//   3. 不给 wrap         保持单行, 超宽硬截断 (右边被切掉)。
//   `wrap` 的文本块默认铺满可用宽度 (否则没有换行依据); 想定宽就显式写 width。
import { h, render } from "gx/gfx";

const lorem =
  "Gox 是一个用纯 Go 实现的 ES6 运行时: 词法分析、语法分析、字节码编译与虚拟机全部手写, " +
  "并自带一套软件光栅化的 GUI 框架 gx/gfx。自动换行是这一版新加的能力: " +
  "中西文按 advance 一视同仁, 一个汉字就是一个断点。";

render(
  <window title="Multiline demo" width={320} height={400}>
    <column gap={6} padding={12}>
      <text font={13} color="#8a8a8a">wrap (260px)</text>
      <text wrap width={260} font={14}>
        {lorem}
      </text>

      <text font={13} color="#8a8a8a">wrap + ellipsis(2)</text>
      <text wrap ellipsis={2} width={260} font={14}>
        {lorem}
      </text>

      <text font={13} color="#8a8a8a">no wrap (clipped)</text>
      <text width={260} font={14}>
        {lorem}
      </text>
    </column>
  </window>
);
