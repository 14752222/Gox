// Gox DevTools Inspector —— GUI 开发期调试面板。
// 三个区：元素树（可展开/选中）+ 节点详情（props/box）、控制台（级别过滤）、
// REPL（devEval，跨调用保持状态）+ 性能摘要（devSnapshot）。
import { h } from "gox";
import { toTreeNodes, parseSelKey, fmtProps, fmtBox, fmtSnapshot } from "./lib/conv.js";
import {
  snap, tree, autoRefresh, setAutoRefresh,
  curWin, setCurWin, logLevel, setLogLevel,
  sel, selNode, selProps, selectNode,
  replInput, setReplInput, replHistory, runRepl,
  filteredLogs, refresh,
} from "./store.js";

const LEVELS = ["all", "log", "info", "warn", "error"];
const levelColor = { log: "#333", info: "#1a5fb4", warn: "#b45309", error: "#b00020" };

// 当前要展示的窗口根节点列表（给 <tree> 的数据）
const winOptions = () => tree().windows.map((w) => `window ${w.id} (scope:${w.scope})`);
const treeNodes = () => {
  const w = tree().windows.find((x) => x.id === curWin()) || tree().windows[0];
  return w ? toTreeNodes(w) : [];
};

export const App = () => (
  <column gap={6} padding={10}>
    {/* ---- 头部：开关 + 窗口切换 + 性能摘要 ---- */}
    <row gap={12}>
      <text font={15}>Gox DevTools</text>
      <switch checked={autoRefresh} onClick={() => setAutoRefresh(!autoRefresh())} />
      <text font={11} color="#889">{() => (autoRefresh() ? "自动刷新 500ms" : "已暂停")}</text>
      <button onClick={() => refresh()}>刷新</button>
      <select
        value={() => `window ${curWin()}`}
        options={() => winOptions()}
        onChange={(e) => setCurWin(Number(String(e.value).split(" ")[1]))}
      />
      <text font={11} color="#b45309">{() => (tree().truncated ? "树已按预算截断" : "")}</text>
    </row>
    <text font={11} color="#555">{() => fmtSnapshot(snap())}</text>

    {/* ---- 元素树 + 节点详情 ---- */}
    <row gap={10}>
      <column gap={4}>
        <text font={12} color="#889">元素树（点击选中，箭头展开）</text>
        <scroll height={280} width={470}>
          <tree
            nodes={() => treeNodes()}
            onSelect={(e) => {
              const s = parseSelKey(e.key);
              if (s) selectNode(s.winId, s.path);
            }}
          />
        </scroll>
      </column>
      <column gap={4}>
        <text font={12} color="#889">
          {() => {
            const s = sel();
            const n = selNode();
            if (!s) return "节点详情（未选中）";
            if (!n) return `节点详情 ${s.path}（节点已消失）`;
            return `节点详情  ${n.tag}  path=${s.path}`;
          }}
        </text>
        <text font={11} color="#246">
          {() => {
            const n = selNode();
            if (!n) return "";
            const bits = [fmtBox(n)];
            if (n.role) bits.push(`role:${n.role}`);
            bits.push(n.focusable ? "focusable" : "not-focusable");
            if (n.key) bits.push(`key:${n.key}`);
            return bits.join("   ");
          }}
        </text>
        <scroll height={248} width={480}>
          <column gap={1}>
            {() => fmtProps(selProps()).map((line) => (
              <text font={11} color="#333">{line}</text>
            ))}
          </column>
        </scroll>
      </column>
    </row>

    {/* ---- 控制台 ---- */}
    <row gap={10}>
      <text font={12} color="#889">console</text>
      <select value={logLevel} options={LEVELS} onChange={(e) => setLogLevel(e.value)} />
      <text font={11} color="#889">{() => `${filteredLogs().length} 条`}</text>
    </row>
    <scroll height={130}>
      <column gap={1}>
        {() => filteredLogs().map((l) => (
          <text font={11} color={levelColor[l.level] || "#333"}>
            {`${l.at} [${l.level}] ${l.text}`}
          </text>
        ))}
      </column>
    </scroll>

    {/* ---- REPL ---- */}
    <text font={12} color="#889">REPL（devEval，跨调用保持状态；回车求值）</text>
    <input
      width={960}
      placeholder="let a = 1  回车，然后  a + 1  回车 → 2"
      value={() => replInput()}
      onInput={(e) => setReplInput(e.value)}
      onKeyDown={(e) => {
        if (e.key === "Enter") {
          runRepl(replInput());
          setReplInput("");
        }
      }}
    />
    <scroll height={110}>
      <column gap={1}>
        {() => replHistory().map((r) => (
          <column gap={0}>
            <text font={11} color="#889">{`${r.at} > ${r.code}`}</text>
            <text font={11} color={r.ok ? "#080" : "#b00020"}>
              {(r.ok ? "= " : "! ") + r.text}
            </text>
          </column>
        ))}
      </column>
    </scroll>
  </column>
);
