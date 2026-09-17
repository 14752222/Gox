// P2-4 演示: 模态对话框 (dialog) 与非模态提示 (toast)。
// 运行: go run . testdata/dialog_demo.js
// 现象: 点 "Open dialog" 弹出居中卡片, 背后整屏压暗; 点遮罩或按 Esc 关闭,
//   点卡片自身不会误关; 遮罩存在时下面的按钮点不动 (遮罩吃掉点击)。
//   点 "Show toast" 在右上角弹出一条绿色提示, 3 秒后自动消失 (JS 侧定时器
//   控制 open signal, 内核只负责层叠渲染正确)。
//   - <dialog open={bool} onClose={fn}> 的流内子节点即内容卡片 (居中);
//   - <toast message level?> 是非模态的: 它下面的内容照常可点。
import { createSignal } from "gx/solid";
import { h, window, render } from "gx/gfx";

const [open, setOpen] = createSignal(false);
const [showToast, setShowToast] = createSignal(false);
const [log, setLog] = createSignal("-");
const [covered, setCovered] = createSignal(0);

// 3 秒后自动收起提示: 只操作 signal, 内核对定时器无感知
const notify = () => {
  setShowToast(true);
  setTimeout(() => setShowToast(false), 3000);
};

const closeBy = (how) => {
  setOpen(false);
  setLog("closed by " + how);
};

render(
  <column gap={10} padding={16}>
    <text font={18}>Dialog and Toast</text>

    <row gap={8}>
      <button onClick={() => setOpen(true)}>Open dialog</button>
      <button onClick={notify}>Show toast</button>
    </row>

    <button
      background="#c0392b"
      color="#ffffff"
      onClick={() => setCovered(covered() + 1)}
    >Covered button</button>

    <text>{() => `dialog: ${open() ? "open" : "closed"}   toast: ${showToast() ? "shown" : "hidden"}`}</text>
    <text>{() => `covered clicks = ${covered()}`}</text>
    <text>{() => log()}</text>

    <dialog open={() => open()} onClose={() => closeBy("mask")}>
      <column gap={8} padding={14}>
        <text font={16}>Confirm</text>
        <text>Click the mask or press Esc to close.</text>
        <button onClick={() => closeBy("button")}>Close</button>
      </column>
    </dialog>

    {() => (showToast() ? <toast message="Saved successfully" level="success"/> : null)}
  </column>,
  window({ title: "Dialog demo", width: 420, height: 320 })
);
