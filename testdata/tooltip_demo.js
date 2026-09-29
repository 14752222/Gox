// P4 演示: 悬停提示 (tooltip)。
// 运行: go run . testdata/tooltip_demo.js
// 现象: 鼠标在按钮上停留约半秒, 旁边弹出深色小字气泡; 移开鼠标、按下鼠标
//   或按 Esc 都会收起。气泡是纯展示弹层 —— 即使它盖在别的控件上, 点击仍然
//   穿过去命中下面的控件 (试试右下角被提示盖住的红按钮)。
//   - <tooltip text placement? delay?> 包裹一个触发元素, 对布局透明:
//     它的盒子就是触发元素的盒子, 想给谁加提示就把它包起来;
//   - placement 贴边放不下会自动翻到另一侧, 再 clamp 回窗口内;
//   - 空串 text 不弹。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";

const [hover, setHover] = createSignal("-");
const [covered, setCovered] = createSignal(0);

render(
  <window title="Tooltip demo" width={420} height={300}>
    <column gap={10} padding={16}>
      <text font={18}>Tooltip</text>

      <row gap={8}>
        <tooltip text="默认方位: bottom, 延迟 500ms">
          <button onClick={() => setHover("default")}>Hover me</button>
        </tooltip>
        <tooltip text="删除后不可恢复" placement="top" delay={200}>
          <button background="#c0392b" color="#ffffff" onClick={() => setHover("red")}>Top placement</button>
        </tooltip>
      </row>

      <tooltip text="保存到云端 (Ctrl+S)" placement="left">
        <button onClick={() => setHover("save")}>Left placement</button>
      </tooltip>

      <text>{() => `last hovered: ${hover()}`}</text>
      <text>{() => `covered clicks = ${covered()}`}</text>

      {/* 提示弹层不挡交互: 把鼠标停在下面按钮的左上角, 等气泡盖上来后,
          点击仍应命中按钮本体 (covered 计数 +1)。 */}
      <tooltip text="点击仍然有效" placement="top">
        <button background="#c0392b" color="#ffffff" width={120} height={30}
          onClick={() => setCovered(covered() + 1)}>Covered button</button>
      </tooltip>
    </column>
  </window>
);
