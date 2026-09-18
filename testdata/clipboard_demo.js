// P3-3 演示: 剪贴板读写 —— 同步 API, 不走 Promise。
// 运行: go run . testdata/clipboard_demo.js
// 现象: 在多行框里打字, 点 "Copy" 把内容放进系统剪贴板 (按钮下方显示成功与否),
//   点 "Paste" 把剪贴板内容读回来替换编辑框内容; 也可以先从别的应用复制一段
//   文字, 直接点 Paste 粘进来。
//   - `clipboardWriteText(s)` 返回布尔 (成功 / 失败), `clipboardReadText()` 返回字符串
//     (读不到时是空串) —— 两者都是**同步**的: 脚本与窗口在同一个 OS 线程,
//     直接调原生 API 即为正确的线程, 不需要 await。
//   - 拿不到剪贴板 (被别的进程占着 / 后端不支持) 时静默降级: 写返回 false、读返回
//     空串, 不抛异常。
// 注意: 非 Windows 后端 (X11) 暂无剪贴板实现, 那里两个按钮分别是 false 与空串。
import { createSignal } from "gx/solid";
import {
  h,
  render,
  clipboardReadText,
  clipboardWriteText,
} from "gx/gfx";

const [text, setText] = createSignal("Hello from Gox");
const [status, setStatus] = createSignal("");

function onCopy() {
  const ok = clipboardWriteText(text());
  setStatus(ok ? "copied" : "copy failed");
}

function onPaste() {
  const s = clipboardReadText();
  setStatus(s === "" ? "clipboard is empty" : "pasted");
  setText(s);
}

render(
  <window title="Clipboard demo" width={360} height={300}>
    <column gap={10} padding={16}>
      <text font={18}>Clipboard</text>

      <textarea
        width={300}
        height={100}
        placeholder="type something"
        value={() => text()}
        onInput={(e) => setText(e.value)}
      />

      <row gap={8}>
        <button onClick={onCopy}>Copy</button>
        <button onClick={onPaste}>Paste</button>
      </row>

      <text>{() => `status: ${status()}`}</text>
      <text>{() => `length = ${text().length}`}</text>
    </column>
  </window>
);
