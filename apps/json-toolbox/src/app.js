// 根组件：菜单栏 + 工具栏 + 左右双栏 + 状态栏。
//
// 快捷键由 Go 侧一张表在**事件泵层**匹配 —— 菜单不必展开就能用（只认带 Ctrl/Alt 的组合，
// 且修饰键全等：Ctrl+S 不会被 Ctrl+Shift+S 触发）。所以 textarea 里的 Ctrl+C / Ctrl+V
// 仍然是编辑框自己的选区复制粘贴，不会被菜单抢走。
import { h } from "gox";
import { colors, space, font } from "./theme.js";
import { Toolbar } from "./components/toolbar.js";
import { StatusBar } from "./components/status-bar.js";
import { Pane } from "./components/pane.js";
import {
  input,
  setInput,
  output,
  setOutput,
  setIndent,
  doFormat,
  doMinify,
  doValidate,
  doClear,
  doCopyOutput,
  doPullOutput,
  doOpen,
  doSaveAs,
} from "./store.js";

const AppMenu = () => (
  <menubar>
    <menu label="文件">
      <menuitem label="打开 JSON…" shortcut="Ctrl+O" onClick={doOpen} />
      <menuitem label="另存结果…" shortcut="Ctrl+S" onClick={doSaveAs} />
    </menu>
    <menu label="编辑">
      <menuitem label="格式化" shortcut="Ctrl+Enter" onClick={doFormat} />
      <menuitem label="压缩成一行" onClick={doMinify} />
      <menuitem label="仅校验" onClick={doValidate} />
      <separator />
      <menuitem label="清空" onClick={doClear} />
    </menu>
    <menu label="缩进">
      <menuitem label="2 个空格" onClick={() => setIndent("two")} />
      <menuitem label="4 个空格" onClick={() => setIndent("four")} />
      <menuitem label="Tab" onClick={() => setIndent("tab")} />
    </menu>
    <menu label="结果">
      <menuitem label="复制结果" shortcut="Ctrl+Shift+C" onClick={doCopyOutput} />
      <menuitem label="结果回填到输入" onClick={doPullOutput} />
    </menu>
    <text font={font.sm} color={colors.muted}>JSON 工具箱 —— Ctrl+Enter 格式化</text>
  </menubar>
);

export function App() {
  return (
    <column background={colors.bg}>
      <AppMenu />
      <Toolbar />

      <row gap={space.md} padding={space.md}>
        <Pane
          title="输入"
          note="粘贴或输入 JSON"
          width={430}
          height={400}
          placeholder='{"a": 1}'
          value={input}
          onChange={setInput}
        />
        <Pane
          title="输出"
          note="格式化 / 压缩的结果"
          width={430}
          height={400}
          placeholder="结果会出现在这里"
          value={output}
          onChange={setOutput}
        />
      </row>

      <StatusBar />
    </column>
  );
}
