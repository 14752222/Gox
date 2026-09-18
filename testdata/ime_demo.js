// P2-7 演示: 输入法 (IME) 输入 —— 候选词提交。
// 运行: go run . testdata/ime_demo.js
// 现象: 点输入框获焦 → 切到中文输入法 → 敲拼音, 正在拼的字由系统自己的组合窗
//   显示 → 选定候选词后**整批**插到光标处, 下面的镜像文本同步更新。
//   - 一次提交只派发一次 onInput (不是每个字一次), 提交次数单独计数;
//   - 提交是追加而非覆盖: 再选一个词, 光标已经跨过整批, 新词接在后面;
//   - 焦点不在 input/textarea 上时输入法会被关掉 —— 在按钮上敲字不弹候选窗;
//   - textarea 里同样可用, 它的光标是二维的 (行 / 列)。
// 注意: 组合过程 (还没选定候选词的那段) 不在输入框内内联绘制, 靠系统组合窗
//   回显; 非 Windows 后端 (X11) 暂无 IME。
// 光标闪烁靠事件泵持续醒来驱动, 这里挂一个空转的 requestAnimationFrame。
import { createSignal } from "gx/solid";
import { h, window, render, requestAnimationFrame } from "gx/gfx";

const [name, setName] = createSignal("");
const [note, setNote] = createSignal("");
const [commits, setCommits] = createSignal(0);

function tick() {
  requestAnimationFrame(tick);
}
tick();

render(
  <column gap={10} padding={16}>
    <text font={18}>IME input</text>

    <input
      width={280}
      placeholder="Switch to a Chinese IME and type"
      value={() => name()}
      onInput={(e) => {
        setName(e.value);
        setCommits((n) => n + 1);
      }}
    />
    <text>{() => `name = "${name()}"`}</text>

    <textarea
      width={280}
      height={80}
      placeholder="multi-line"
      value={() => note()}
      onInput={(e) => setNote(e.value)}
    />
    <text>{() => `note = "${note()}"`}</text>

    <text>{() => `ime commits = ${commits()}`}</text>
  </column>,
  window({ title: "IME demo", width: 360, height: 360 })
);
