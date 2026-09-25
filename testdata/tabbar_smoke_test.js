// ===== TabBar 组件库 headless 挂载冒烟测试 =====
// 运行: go run . testdata/tabbar_smoke_test.js
// 验证: AppShell 真实挂载 (桌面 SideNav 形态) → resize 跨断点切到 TabBar 形态
// → 再切回来 → 全部阶段无脚本错误 → 关窗退出。纯逻辑断言在 tabbar_logic_test.js。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";
import { createRouter, RouterView } from "gx/router";
import { AppShell, createTabs } from "./ui/shell.js";
import { icons } from "./ui/icons.js";

const [unread, setUnread] = createSignal(2);

function HomePage() {
  return <text font={14}>home</text>;
}
function DiscoverPage() {
  return (
    <column gap={6} padding={12}>
      <text font={14}>discover</text>
      <button onClick={() => setUnread(0)}>clear</button>
    </column>
  );
}

const tabs = createTabs([
  { path: "/home", title: "Home", icon: icons.home },
  { path: "/discover", title: "Chat", icon: icons.chat, badge: () => unread() },
  { path: "/mine", title: "Mine", icon: icons.user },
]);

const router = createRouter({
  routes: [
    { path: "/home", name: "home", component: HomePage, keepAlive: true },
    { path: "/discover", name: "discover", component: DiscoverPage, keepAlive: true },
    { path: "/mine", name: "mine", component: HomePage, keepAlive: true },
    { path: "*", name: "nf", component: HomePage },
  ],
  initial: "/home",
});

let stage = 0;
const win = render(
  <window title="tabbar smoke" width={960} height={640}>
    <AppShell tabs={tabs} router={router} breakpoint={720} content={() => <RouterView />} />
  </window>
);

// 阶段推进: 挂载(桌面形态) → 缩窄(TabBar 形态) → 拉宽(SideNav 形态) → 退出。
// 任何阶段抛脚本错误, stderr 会有输出且 PASS 不会打全。
setTimeout(() => {
  stage = 1;
  console.log("STAGE 1 mounted (wide/desktop body)");
  win.resize(400, 600);
}, 100);
setTimeout(() => {
  stage = 2;
  console.log("STAGE 2 resized narrow (mobile body + TabBar)");
  setUnread(5); // badge 变化路径
  win.resize(960, 640);
}, 300);
setTimeout(() => {
  stage = 3;
  console.log("STAGE 3 resized wide again (desktop body)");
  console.log("SMOKE ALL PASS (stage=" + stage + ")");
  win.close(); // 全部窗口关闭 → 事件循环退出 (之后的定时器不会再跑)
}, 500);
