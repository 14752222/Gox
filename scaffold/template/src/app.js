// 根组件：把各面板组合起来。
//
// 页签切换用 `show` 指令（元素级指令，写在元素上，对标 v-show）—— 它是
// **keep-alive**：隐藏只是把子树摘出布局流，节点与状态都保活。所以在计数器页签
// 调过的值、在输入框里打的字，切走再切回来都还在。
//
// 想要"每次显示都全新构建"（v-if）就换函数子节点：{() => tab() === 0 ? <Counter/> : null}。
import { h } from "gox";
import { colors, space, font } from "./theme.js";
import { tab, setTab } from "./store.js";
import { Counter } from "./components/counter.js";
import { TodoList } from "./components/todo-list.js";
import { StatusBar } from "./components/status-bar.js";

const TABS = ["计数器", "待办列表", "状态"];

// 页签按钮：按当前页签换底色。注意 background / color 传的是**取值函数** ——
// 传快照（colors.accent 这种）只会在构建那一刻求值一次，点别的页签不会变。
const TabButton = (p) => (
  <button
    padding={5}
    background={() => (tab() === p.index ? colors.accent : colors.panel)}
    color={() => (tab() === p.index ? colors.accentFg : colors.text)}
    onClick={() => setTab(p.index)}
  >
    {p.label}
  </button>
);

export function App() {
  return (
    <column gap={space.md} padding={space.lg}>
      <text font={font.xl} color={colors.text}>__PROJECT_TITLE__</text>

      <row gap={space.sm} alignItems="center">
        {TABS.map((name, i) => <TabButton label={name} index={i} />)}
      </row>

      <separator />

      {/* 三个面板都常驻（keep-alive），靠 show 指令显隐。
          ⚠️ children 位置写的是 {Counter} 而不是 <Counter/>：JSX 里大写标签是
          **当场调用**，放进指令容器的 children 就等于"脚本求值期就跑了一遍组件体"。 */}
      <view show={() => tab() === 0}>{Counter}</view>
      <view show={() => tab() === 1}>{TodoList}</view>
      <view show={() => tab() === 2}>{StatusBar}</view>

      <spacer flexGrow={1} />
      <separator />
      <text font={font.sm} color={colors.muted}>
        JSX + signal · 改 src/ 下的文件后重跑即可
      </text>
    </column>
  );
}
