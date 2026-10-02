// [M3] DevTools Inspector v1 演示: 脚本自绘一个"元素树 + 日志 + REPL"面板。
// 运行: ./gox testdata/devtools_demo.js
//
// 现象:
//   1. 顶部 "element tree" 区每秒拉一次 devTree(), 按缩进展示每个节点的
//      tag / key / 布局尺寸; 被有界截断的节点带 "…" 标记 (面板不会撑爆)。
//   2. 中部 "console" 区展示最近 30 条 console 输出 (devLogs 留存);
//      你自己在脚本里 console.log 一行, 这里也能看到 (stdout 也照旧打印)。
//   3. 底部输入框是 REPL: 回车 → devEval(输入); 结果或错误显示在 "=" 一行。
//      REPL 是**跨调用保持状态**的: 先 `let a = 1` 回车, 再 `a + 1` 回车得 2。
//
// 要点 (docs/gui-patterns.md §5 / docs/devtools.md):
//   - 拉取式刷新: setInterval 1s 拉一次, 不推送; **别用 requestAnimationFrame**
//     (会和真实渲染抢帧 —— 面板是观察者, 不该参与帧预算)。
//   - 面板就是普通脚本: gx/dev 是公共数据面, 想看什么自己加一行。
//   - REPL 环境: 宿主若在进事件循环前调过 gfx.SetDevEnv(vm.Globals()),
//     devEval 就在**真实 app 环境**里求值 (能读到 app 自己的变量); 没接线时
//     退化为独立沙盒 (能跑, 但看不到 app 变量)。见 docs/devtools.md "接线点"。
import { createSignal } from "gx/solid";
import { devTree, devLogs, devEval } from "gx/dev";
import { h, render } from "gx/gfx";

// ---- 数据面: 每秒拉一次 (有界, 免得把面板自己撑爆) ----
const [tree, setTree] = createSignal(devTree({ maxDepth: 5, maxNodes: 300 }));
const [logs, setLogs] = createSignal(devLogs("", 30));
setInterval(() => {
  setTree(devTree({ maxDepth: 5, maxNodes: 300 }));
  setLogs(devLogs("", 30));
}, 1000);

// ---- 把节点树展平成缩进文本行 (纯展示, 不做交互) ----
const flatten = (node, depth) => {
  const indent = "  ".repeat(depth);
  const key = node.key ? `#${node.key}` : "";
  const cut = node.truncated ? " …" : "";
  const rows = [`${indent}${node.tag}${key}  [${node.box.w}x${node.box.h}]${cut}`];
  for (const child of node.children) rows.push(...flatten(child, depth + 1));
  return rows;
};

const treeLines = () => {
  const t = tree();
  const lines = [];
  for (const win of t.windows) lines.push(...flatten(win.root, 0));
  return lines;
};

// ---- REPL 状态 ----
const [input, setInput] = createSignal("");
const [result, setResult] = createSignal("(输入表达式后回车)");

const runRepl = () => {
  const code = input();
  if (!code) return;
  const r = devEval(code);
  setResult(r.ok ? "= " + r.value : "! " + r.error);
  console.log("repl>", code); // 顺带进 console 留存, 面板上能看到
  setInput("");
};

render(
  <window title="gx/dev inspector" width={580} height={480}>
    <column gap={8} padding={12}>
      <text font={15}>Inspector v1 (1s pull)</text>

      <text font={12} color="#889">element tree (devTree, depth 5 / 300 nodes)</text>
      <scroll height={140}>
        <column gap={1}>
          {() => treeLines().map((line) => <text font={11} color="#246">{line}</text>)}
        </column>
      </scroll>

      <text font={12} color="#889">console ({() => logs().length})</text>
      <scroll height={90}>
        <column gap={1}>
          {() => logs().map((l) => <text font={11} color="#b00020">{`${l.at} [${l.level}] ${l.text}`}</text>)}
        </column>
      </scroll>

      <input
        width={540}
        placeholder="REPL: let a = 1  然后  a + 1"
        value={() => input()}
        onInput={(e) => setInput(e.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") runRepl();
        }}
      />
      <text font={12} color="#080">{() => result()}</text>
    </column>
  </window>
);
