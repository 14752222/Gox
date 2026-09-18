// 路由 A 模式演示: 用户态 signal 切页 (2026-09-19 拍板落地)。
// 运行: go run . testdata/routing_demo.js
// 现象:
//   1. 底部两个导航按钮切 "home" / "editor" 两页; 顶部文本显示当前路由。
//   2. editor 页输入文字后按 "home": 被**拦截**, 出现应用内确认条
//      ("未保存" + 留下/放弃) —— 守卫是模式层的普通函数, 内核零参与。
//   3. "留下" 回编辑器且草稿不丢; "放弃" 才真的切页, 且草稿清空。
//   4. editor 干净时 (没改过 / 已保存) 切页直接走, 不弹确认。
//
// 模式要点 (完整文档见 docs/gui-patterns.md §1-§2):
//   - 路由就是一个 signal: 页面表是普通对象, 当前页是 memo。
//   - **切页即卸载** (v1 拍板): editor 不挂载时 input 连节点都不存在 ——
//     想保留草稿就把状态提到页面组件外面 (这里的 draft/saved 就是)。
//   - 未保存拦截 = go() 里的一个 if: 脏了就先把目标页挂到 pending
//     signal 上, 确认条的按钮再决定放不放行。不需要内核 hook。
//   - 切页转场直接给页面根元素挂 transition (P3-2), 本演示为可读性省略,
//     写法见文档 §2。
import { createSignal, createMemo } from "gx/solid";
import { h, render } from "gx/gfx";

// ---- 路由状态 (全部在页面组件之外: 切页卸载不丢) ----
const [route, setRoute] = createSignal("home");
const [pending, setPending] = createSignal(null); // 被守卫拦下的目标页
const [draft, setDraft] = createSignal("");
const [saved, setSaved] = createSignal("");
const dirty = createMemo(() => draft() !== saved());

// 守卫: 所有切页都走这里, 脏 editor 想离开 → 先拦截。
function go(next) {
  if (route() === "editor" && next !== "editor" && dirty()) {
    setPending(next); // 只改 signal, 确认条是被它驱动的响应式子树
    return;
  }
  setRoute(next);
  if (next !== "editor") { setDraft(""); setSaved(""); } // 离开即清稿
}
function stay() { setPending(null); }
function leave() {
  const t = pending();
  setPending(null);
  setDraft(""); setSaved(""); // 放弃修改: 不清稿会被自己的守卫再拦一次
  go(t);
}

// ---- 页面表: 普通 JS 对象, 值是返回元素的函数 ----
const pages = {
  home: () => (
    <column gap={8}>
      <text font={15} wrap>Home. Nothing to guard here - switch freely.</text>
      <rect height={8} width={220} background="#3355aa" />
    </column>
  ),
  editor: () => (
    <column gap={8}>
      <input width={260} value={() => draft()} onInput={(e) => setDraft(e.value)} />
      <button onClick={() => setSaved(draft())}>Save</button>
    </column>
  ),
};

render(
  <window title="routing demo" width={420} height={300}>
    <column gap={10} padding={14}>
      <text font={13} color="#889">{() => "route: " + route() + (dirty() ? " (dirty)" : "")}</text>

      {/* 条件渲染的统一入口: 函数子节点。确认条出现时盖在页面区上方 */}
      {() => pending() ? (
        <row gap={8} background="#fdf3e3" padding={8}>
          <text font={13}>Unsaved changes</text>
          <button onClick={stay}>Stay</button>
          <button onClick={leave}>Discard & go</button>
        </row>
      ) : null}

      {() => pages[route()]()}

      <row gap={8}>
        <button onClick={() => go("home")}>home</button>
        <button onClick={() => go("editor")}>editor</button>
      </row>
    </column>
  </window>
);
