// 布局弹性词汇演示: 百分比 / min-max / flexShrink (2026-09-19 落地)。
// 运行: go run . testdata/elastic_layout_demo.js
// 现象 (拖动窗口边缘, 全程零 JS 参与):
//   1. 顶部三张卡片 width="30%": 连续跟随窗口宽度 (与 resize_demo 的断点
//      切换互补 —— 断点是离散适配, 百分比是连续适配)。
//   2. 中部一行工具按钮: 窗口变窄到放不下时, 标了 flexShrink 的按钮按
//      "系数×基础尺寸" 加权收缩, 没标的保持原样溢出。
//   3. 底部进度条 flexGrow 拉伸但 maxWidth=360 钳位 ("拉伸但有上限");
//      再窄时 minWidth=160 兜底。
//
// 词汇表 (gui-component-status.md §3.4 / gui-responsive-screen-options.md §3.4):
//   width="50%" / height="50%"   按父容器内容区解析; 百分比子节点不撑大父容器
//   minWidth/maxWidth/minHeight/maxHeight   终钳位 (在 stretch/grow/shrink 之后)
//   flexShrink   主轴溢出时按 系数×基础尺寸 加权收缩 (flexGrow 的对称面)
//   仍不支持: 容器级 wrap / order / alignSelf
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";

const [win, setWin] = createSignal({ width: 520, height: 360 });

const Card = (p, ...kids) => (
  <column width="30%" gap={4} background="#eef1f6" padding={8}>
    {kids}
  </column>
);

render(
  <window title="elastic layout" width={520} height={360}>
    <column gap={12} padding={14} onResize={(e) => setWin({ width: e.width, height: e.height })}>
      <text font={16}>{() => `window ${win().width}x${win().height} — everything below adapts declaratively`}</text>

      {/* ① 百分比卡片: 连续适配 */}
      <row gap={10}>
        <Card><text font={13}>30%</text><text font={12} color="#68707c">percent card</text></Card>
        <Card><text font={13}>30%</text><text font={12} color="#68707c">percent card</text></Card>
        <Card><text font={13}>30%</text><text font={12} color="#68707c">percent card</text></Card>
      </row>

      {/* ② flexShrink: 变窄时压缩 (前两个固定, 后两个收缩) */}
      <column gap={4}>
        <text font={13}>shrink row (narrow the window):</text>
        <row gap={8}>
          <button>fixed</button>
          <button>fixed</button>
          <rect width={140} height={26} background="#3355aa" flexShrink={1} />
          <rect width={220} height={26} background="#2b7a4b" flexShrink={2} />
        </row>
      </column>

      {/* ③ grow + min/max: 拉伸但有底线/上限 */}
      <column gap={4}>
        <text font={13}>grow with maxWidth=360 / minWidth=160:</text>
        <rect height={10} background="#c0392b" flexGrow={1} maxWidth={360} minWidth={160} />
        <rect height={10} background="#3355aa" width="100%" />
      </column>

      {/* ④ wrap: 标签流 (变窄自动换行, 容器高度自动回填) */}
      <column gap={4}>
        <text font={13}>wrap tag flow (narrow the window):</text>
        <row wrap gap={6}>
          {["go", "gui", "flex", "wrap", "percent", "shrink", "min-max", "token", "reactive", "desktop", "mobile"].map((tag) => (
            <column background="#e8ecf4" padding={4}>
              <text font={12} color="#3355aa">{`#${tag}`}</text>
            </column>
          ))}
        </row>
      </column>
    </column>
  </window>
);
