// 工具栏：动作按钮 + 缩进选择 + 路径查询。
import { h } from "gox";
import { colors, space, font } from "../theme.js";
import {
  doFormat,
  doMinify,
  doValidate,
  doClear,
  doCopyOutput,
  doPullOutput,
  doQuery,
  indent,
  setIndent,
  path,
  pathOut,
} from "../store.js";

const INDENTS = [
  { value: "two", label: "缩进：2 空格" },
  { value: "four", label: "缩进：4 空格" },
  { value: "tab", label: "缩进：Tab" },
];

export const Toolbar = () => (
  <column gap={space.sm} padding={space.md} background={colors.panel}>
    <row gap={space.sm} alignItems="center">
      <button padding={5} onClick={doFormat}>格式化</button>
      <button padding={5} onClick={doMinify}>压缩</button>
      <button padding={5} onClick={doValidate}>仅校验</button>
      <button padding={5} onClick={doClear}>清空</button>

      {/* select 是受控的：显示看 value，选中只派发 onChange */}
      <select
        width={150}
        options={INDENTS}
        value={() => indent()}
        onChange={(e) => setIndent(e.value)}
      />

      <button padding={5} onClick={doCopyOutput}>复制结果</button>
      <button padding={5} onClick={doPullOutput}>结果回填输入</button>
    </row>

    <row gap={space.sm} alignItems="center">
      <text font={font.sm} color={colors.muted}>路径查询</text>
      {/* model 一条指令接好读（value）与写（onInput）两个方向 */}
      <input
        width={240}
        placeholder="例如 gui.backends[1]"
        model={path}
        onKeyDown={(e) => {
          if (e.key === "Enter") doQuery();
        }}
      />
      <button padding={5} onClick={doQuery}>查询</button>
      <text font={font.sm} color={colors.accent} wrap={true} width={340}>{pathOut}</text>
    </row>
  </column>
);
