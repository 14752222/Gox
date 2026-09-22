// 派生值 + 多分支 + 读本页路由。
//
// 派生值：`已完成 x / y` 这种不需要第二份 signal —— 写成读 signal 的普通函数，
// 读它的那个文本节点会自动跟着更新。两处状态必然对不上，别存两份。
//
// 多分支：<Switch> / <Match when={...}>（对标 v-if / v-else-if / v-else）。
// · when 收的是**取值函数**，写成 when={is("ready")} 只是当场的布尔快照，之后切不回来。
// · Match 分支同样是 keep-alive：隐藏 = 摘出布局流，子树保活（切走再切回来状态原样）。
//
// 读路由：useRoute() 给出**本页所属那条路由**的取值函数（signal 语义）。要在页面体里
// 读它，而不是在窗口根上读 router.currentRoute() —— 多窗口下后者读的是"最近用到的会话"。
import { h, createSignal, Switch, Match, useRoute } from "gox";
import { colors, space, font } from "../theme.js";
import { todos } from "../store.js";

export function StatusBar() {
  const route = useRoute();
  const [phase, setPhase] = createSignal("ready");
  const is = (name) => phase() === name;
  const done = () => todos().filter((t) => t.done).length;

  return (
    <column gap={space.sm}>
      <text font={font.sm} color={colors.muted}>
        {() => "本页路由 " + route().path + "（useRoute 写在页面体里，随导航更新）"}
      </text>

      <separator />

      <text font={font.md} color={colors.text}>派生值（由 todos 现算，不另存状态）</text>
      <text font={font.sm} color={colors.muted}>
        {() => "已完成 " + done() + " / " + todos().length + " 项"}
      </text>

      <separator />

      <text font={font.md} color={colors.text}>多分支（Switch / Match）</text>
      <row gap={space.sm} alignItems="center">
        <button padding={4} disabled={() => is("loading")} onClick={() => setPhase("loading")}>loading</button>
        <button padding={4} disabled={() => is("ready")} onClick={() => setPhase("ready")}>ready</button>
        <button padding={4} disabled={() => is("error")} onClick={() => setPhase("error")}>error</button>
      </row>

      <Switch
        fallback={<text font={font.sm} color={colors.warn}>没有 Match 命中 ⇒ fallback 分支</text>}
      >
        <Match when={() => is("loading")}>
          <row gap={space.sm} alignItems="center">
            <progress value={0.5} width={160} />
            <text font={font.sm} color={colors.muted}>加载中…</text>
          </row>
        </Match>
        <Match when={() => is("ready")}>
          <text font={font.sm} color={colors.ok}>就绪 —— 分支保活，切走再切回来是同一批节点</text>
        </Match>
        <Match when={() => is("error")}>
          <text font={font.sm} color={colors.danger}>出错了（分支只构建一次，状态不会重置）</text>
        </Match>
      </Switch>
    </column>
  );
}
