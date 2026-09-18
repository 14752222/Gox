// P2-6 演示: 多行文本编辑 <textarea>。
// 运行: go run . testdata/textarea_demo.js
// 现象: 点击编辑框获焦 (边框转蓝 + 闪烁竖线光标) ——
//   - 直接敲字母/汉字即插入 (BMP 直输, 中文 IME 见 P2-7);
//   - Enter **插入换行**(多行框里 Enter 是内容, 不像单行 input 那样放行给上层),
//     上下左右/Home/End 在二维上移动光标, Backspace 在行首会与上一行合并;
//   - 内容超过 4 行后自动纵向滚动, 且**滚动跟随光标**(在底部回车时光标不会
//     跑到框外); 也可以把光标放进框里滚滚轮;
//   - Esc 不被编辑框消费, 会冒泡到 onKeyDown (这里用来计数)。
// 受控: 编辑只派发 onInput({value}), 显示永远来自 value —— 底下那行镜像
// 文本就是 value 本身。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";

const [text, setText] = createSignal("");
const [escapes, setEscapes] = createSignal(0);
const [keys, setKeys] = createSignal(0);

render(
  <window title="Textarea demo" width={320} height={280}>
    <column gap={8} padding={12}>
      <text font={16}>Textarea</text>

      <textarea
        rows={4}
        width={260}
        placeholder="Type here..."
        value={() => text()}
        onInput={(e) => setText(e.value)}
        onKeyDown={(e) => {
          setKeys((n) => n + 1);
          if (e.key === "Escape") setEscapes((n) => n + 1);
        }}
      />

      <text wrap width={260} font={12}>
        {() => `value = "${text()}"`}
      </text>
      <text font={12}>{() => `escapes = ${escapes()} keys = ${keys()}`}</text>
    </column>
  </window>
);
