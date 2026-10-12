// 根组件：显示屏 + 4×5 按钮网格。
import { h } from "gox";
import { colors, space, font } from "./theme.js";
import { display, tap, key } from "./store.js";
import { KEYS } from "./lib/calc.js";

// KeyButton: 一个按键。kind 决定配色（数字深灰 / 功能浅灰 / 运算橙）。
const KeyButton = (p) => {
  const k = p.k;
  return (
    <button
      width={88}
      height={64}
      font={font.lg}
      background={() => (k.kind === "op" ? colors.op : k.kind === "fn" ? colors.fn : colors.digit)}
      color={() => (k.kind === "fn" ? colors.fnText : colors.text)}
      onClick={() => tap(k.label)}
    >{k.label}</button>
  );
}

export function App() {
  return (
    <column
      background={colors.bg}
      gap={space.sm}
      padding={space.md}
      onKeyDown={(e) => key(e.key)}
    >
      {/* 显示屏：右对齐大数字 */}
      <row width={358} height={120} justifyContent="end" alignItems="center" padding={space.md}>
        <text font={font.xl} color={colors.text}>{display}</text>
      </row>
      <grid columns={4} gap={space.sm}>
        <view each={() => KEYS} key="label">
          {(k) => <KeyButton k={k} />}
        </view>
      </grid>
    </column>
  );
}
