// 一个编辑面板：标题行 + 多行编辑框。
//
// 编辑框是**受控**的：显示只看 value，改动只派发 onInput —— 不回写就不会有反应。
// value 收的是取值函数（signal 本身就是函数），写成 value={p.value()} 只是第一帧的快照。
import { h } from "gox";
import { colors, space, font } from "../theme.js";

export const Pane = (p) => (
  <column gap={space.xs} width={p.width}>
    <row gap={space.sm} alignItems="center" width={p.width}>
      <text font={font.sm} color={colors.text}>{p.title}</text>
      <spacer flexGrow={1} />
      <text font={font.xs} color={colors.muted}>{p.note}</text>
    </row>
    <textarea
      width={p.width}
      height={p.height}
      fontFamily="monospace"
      font={font.md}
      placeholder={p.placeholder}
      value={() => p.value()}
      onInput={(e) => p.onChange(e.value)}
    />
  </column>
);
