// P3-6 演示: 多窗口。
// 运行: go run . testdata/multiwindow_demo.js
// 现象:
//   1. 同时打开两个独立窗口 (Counter A / Counter B), 各自有自己的计数按钮与文本。
//      点 A 的按钮只改 A 的数字, B 完全不受影响 (元素树、焦点、交互态都是**每窗口一份**)。
//   2. 每个窗口底部有一个 "Close this window" 按钮:
//      关掉其中一个, 另一个继续正常响应 (它的计数还能往上走);
//      两个都关掉, 进程才退出 (事件循环由 Pump 统一驱动, 全部窗口关闭才结束)。
//   3. Ctrl+Q 也可以关当前窗口 (走的是**每窗口各自**的全局快捷键表 ——
//      在哪个窗口按键就关哪个窗口)。
//
// 要点 (写在代码里也写在文档里):
//   - render() 现在**返回窗口句柄**, 带 close(): 多窗口下"关掉某一个"是刚需,
//     而脚本除了返回值之外没有别的办法指认是哪个窗口。
//   - close() 内部经 Post 投回 GUI 线程 (窗口销毁必须在创建它的线程上做),
//     所以调用后不会立即生效, 要等本轮事件处理跑完。
//   - 每个窗口是独立的 app 实例: 悬停链、按压态、键盘焦点、快捷键表、
//     弹层状态互不干扰。gfx.Post 的任务队列是全局的 (v1 广播), 每轮只排空一次。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";

// makeCounter 造一个窗口: 标题不同、计数独立、带自己的关闭按钮。
// 复用同一个组件定义是刻意的 —— 它证明"同一份脚本代码可以挂成多个窗口",
// 每个窗口拿到的是独立的一份运行状态 (createSignal 在组件里调, 每次一套)。
const makeCounter = (title, accent) => {
  const [n, setN] = createSignal(0);
  let self = null; // 先占位: 句柄要等 render 返回才拿得到

  const close = () => {
    if (self) self.close();
  };

  self = render(
    <window title={`Multi-window ${title}`} width={380} height={340}>
      <column>
        <menubar>
          <menu label="File">
            <menuitem label="Close window" shortcut="Ctrl+Q" onClick={close} />
          </menu>
          <text>ready</text>
        </menubar>

        <column gap={12} padding={16}>
          <text font={18}>Window {title}</text>
          <text>Each window has its own element tree, focus and shortcuts.</text>

          <rect width={260} height={8} background={accent} />

          <row gap={10}>
            <button onClick={() => setN(n() + 1)}>+1</button>
            <button onClick={() => setN(n() - 1)}>-1</button>
            <button onClick={() => setN(0)}>reset</button>
          </row>

          <text font={22}>{() => `${title} count = ${n()}`}</text>

          <text>Tip: focus this window and press Ctrl+Q, or use File - Close window.</text>
          <button onClick={close}>Close this window</button>
        </column>
      </column>
    </window>
  );

  return self;
};

const a = makeCounter("A", "#c0392b");
const b = makeCounter("B", "#2b7a4b");

// 把句柄留在全局, 方便在控制台/调试器里手动关 (脚本本身不需要)。
// 末尾用一根空语句收尾: Gox 会打印脚本最后一个表达式的值, 不收的话
// 这里会往终端回显一行 `{ close: [Function: close], ... }` 的噪音。
globalThis.windowA = a;
globalThis.windowB = b;
void 0;
