// P1-3 演示: 焦点系统 (onFocus / onBlur + 虚线焦点框)。
// 运行: go run . testdata/focus_demo.js
// 现象: 点击任一块 → 它获得蓝色虚线焦点框, 上一块失去焦点; 底部记录事件顺序。
//   - 焦点框画在焦点节点自身盒内 (1px 内缩), 所以局部重绘能干净擦掉旧框;
//   - 根节点接焦时没有焦点框; 根节点 props.hideFocusRing 可整体关闭。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";

const [focused, setFocused] = createSignal("(none)");
const [log, setLog] = createSignal("(click a block)");
const [ring, setRing] = createSignal(true);

// 焦点回调沿祖先链上浮: 处理器挂在哪一层都能收到
const block = (name) => (
  <button
    onClick={() => setLog(`click ${name}`)}
    onFocus={() => { setFocused(name); setLog(`focus -> ${name}`); }}
    onBlur={() => setLog(`blur <- ${name}`)}
  >
    {name}
  </button>
);

render(
  <window title="Focus demo" width={420} height={260}>
    <column gap={12} padding={16} hideFocusRing={() => !ring()}>
      <text font={18}>Focus ring</text>

      <row gap={12} alignItems="center">
        {block("Alpha")}
        {block("Beta")}
        {block("Gamma")}
      </row>

      <text>{() => `focused: ${focused()}`}</text>
      <text>{() => `log: ${log()}`}</text>

      <row gap={8} alignItems="center">
        <checkbox checked={() => ring()} onClick={() => setRing(v => !v)}/>
        <text>show focus ring</text>
      </row>
    </column>
  </window>
);
