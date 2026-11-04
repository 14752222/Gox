// 数据面：拉取式刷新（gx/dev 是只读快照面，不推送）。
// 纪律：面板是观察者，不参与帧预算 —— 用 setInterval，不用 requestAnimationFrame。
import { createSignal } from "gox";
import { devSnapshot, devTree, devNode, devProps, devLogs, devEval } from "gx/dev";

// ---- 快照信号 ----
export const [snap, setSnap] = createSignal(null);
export const [tree, setTree] = createSignal({ windows: [], truncated: false });
export const [logs, setLogs] = createSignal([]);

// ---- 面板状态 ----
export const [autoRefresh, setAutoRefresh] = createSignal(true);
export const [curWin, setCurWin] = createSignal(0); // 0 = 跟随第一扇窗
export const [logLevel, setLogLevel] = createSignal("all");
export const [sel, setSel] = createSignal(null); // { winId, path }
export const [selNode, setSelNode] = createSignal(null);
export const [selProps, setSelProps] = createSignal(null);

// ---- REPL ----
export const [replInput, setReplInput] = createSignal("");
export const [replHistory, setReplHistory] = createSignal([]); // [{code, ok, text, at}]

const now = () => new Date().toTimeString().slice(0, 8);

// refresh 拉一帧快照。有界：深度 8 / 节点 800，面板不能把自己撑爆。
export const refresh = () => {
  setSnap(devSnapshot());
  const t = devTree({ maxDepth: 8, maxNodes: 800 });
  setTree(t);
  setLogs(devLogs("", 200));
  // 选中节点跟着刷新（布局/props 可能变了）；窗口没了就丢选中。
  const s = sel();
  if (s) {
    setSelNode(devNode(s.path, s.winId));
    setSelProps(devProps(s.path, s.winId));
  }
  if (curWin() === 0 && t.windows.length > 0) setCurWin(t.windows[0].id);
};

// selectNode 由 tree 的 onSelect 触发。
export const selectNode = (winId, path) => {
  setSel({ winId, path });
  setSelNode(devNode(path, winId));
  setSelProps(devProps(path, winId));
};

// runRepl 求值并落历史（上限 100 条，新条目在前）。
export const runRepl = (code) => {
  if (!code) return;
  const r = devEval(code);
  const entry = { code, ok: r.ok, text: r.ok ? r.value : r.error, at: now() };
  setReplHistory([entry, ...replHistory()].slice(0, 100));
  console.log("repl>", code); // 顺带进 console 留存，控制台面板能看到
};

// 过滤后的日志（面板侧过滤，留存缓冲本身是只读环形，清不空也不想清）。
export const filteredLogs = () => {
  const lv = logLevel();
  const all = logs();
  const out = lv === "all" ? all : all.filter((l) => l.level === lv);
  return out.slice(-100);
};

// 启动：立刻拉一次，然后 500ms 轮询（demo 用 1s；交互面板跟手一点）。
refresh();
setInterval(() => {
  if (autoRefresh()) refresh();
}, 500);
