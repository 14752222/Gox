// 底部状态栏：结果消息（按 kind 上色）、错误位置、两侧文本统计。
//
// ⚠️ kindColor 之类"模块级函数引用 import 绑定"的写法在这里不成立：signal 变化触发
// effect 时，跨一层函数再引用 import 的 `colors` 会报 `colors is not defined`
// （初始渲染正常、重渲染时失败 —— 已最小化定位）。所以颜色选择**内联在回调闭包里**，
// 让 `colors` 的引用发生在 depth 1。改这段时别把三元抽回辅助函数。
import { h } from "gox";
import { colors, space, font } from "../theme.js";
import { status, statusKind, where, input, output } from "../store.js";
import { statsOf } from "../lib/json.js";

export const StatusBar = () => (
  <row gap={space.md} padding={space.sm} background={colors.panel} alignItems="center">
    {/* signal 直接放在 children 位置即为响应式文本 */}
    <text
      font={font.sm}
      color={() =>
        statusKind() === "ok" ? colors.ok : statusKind() === "error" ? colors.danger : colors.muted
      }
    >
      {status}
    </text>

    {/* show 是 keep-alive 的显隐：没有位置信息时整块摘出布局流，不留空隙 */}
    <view show={() => where() !== ""}>
      <text font={font.sm} color={colors.danger}>{where}</text>
    </view>

    <text font={font.xs} color={colors.muted}>
      {() => "输入 " + statsOf(input()).lines + " 行 / " + statsOf(input()).chars + " 字符"}
    </text>
    <text font={font.xs} color={colors.muted}>
      {() => "输出 " + statsOf(output()).lines + " 行 / " + statsOf(output()).chars + " 字符"}
    </text>
  </row>
);
