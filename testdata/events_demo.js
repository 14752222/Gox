// P1-1 演示: 鼠标移动 / 滚轮 / 右键 / 键盘抬起 / 修饰键。
// 运行: go run . testdata/events_demo.js
// 现象: 在浅蓝框内移动鼠标 → 坐标实时刷新; 滚轮 / 右键 / 按键各自记录一行。
//   - 键事件路由给"当前焦点节点": 先点一下浅蓝框, 焦点才会落到它身上;
//   - onWheel 的 deltaY 沿用 DOM 约定 (向下滚为正), 与 Go 事件层的
//     DeltaY (向上为正, Windows 原生语义) 符号相反。
import { createSignal } from "gx/solid";
import { h, window, render } from "gx/gfx";

const [pos, setPos] = createSignal("(move the mouse)");
const [last, setLast] = createSignal("(no event yet)");

// 把修饰键拼成 " +Ctrl+Shift+Alt" 形式, 便于一眼看出按下了什么
function mods(e) {
  let s = "";
  if (e.ctrl) s += "+Ctrl";
  if (e.shift) s += "+Shift";
  if (e.alt) s += "+Alt";
  return s;
}

render(
  <column gap={10} padding={16}>
    <text font={18}>Mouse & key events</text>

    <rect
      width={380}
      height={110}
      background="#eef3f8"
      border="#8aa0b6"
      onClick={() => setLast("click")}
      onMouseMove={(e) => setPos(`${e.x}, ${e.y}`)}
      onWheel={(e) => setLast(`wheel deltaY=${e.deltaY}`)}
      onContextMenu={(e) => setLast(`context menu at ${e.x}, ${e.y}`)}
      onKeyDown={(e) => setLast(`keydown ${e.key}${mods(e)}`)}
      onKeyUp={(e) => setLast(`keyup ${e.key}${mods(e)}`)}
    >
      <text>click to focus, then move / scroll / right-click / type</text>
    </rect>

    <text>{() => `position: ${pos()}`}</text>
    <text>{() => `last event: ${last()}`}</text>
  </column>,
  window({ title: "Events demo", width: 420, height: 240 })
);
