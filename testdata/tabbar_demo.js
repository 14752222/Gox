// ===== TabBar / SideNav / AppShell 综合演示 (PC / 移动自适应导航壳) =====
// 运行: go run . testdata/tabbar_demo.js
//
// 现象:
//   1. 桌面 (窗口宽 ≥ 720): 左侧 SideNav —— 竖排项、选中项左指示条、
//      底部"设置"低频项 (spacer 沉底)、《折叠》按钮 (宽度 transition 动画,
//      折叠态记忆到 gx/storage, 重启还在)。
//   2. 把窗口拖窄 (< 720) 或拖宽: 根节点 onResize → 断点分派自动在
//      SideNav ↔ 底部 TabBar 之间切换, 业务代码零改动。
//   3. Ctrl+1..4 直接切 tab (键盘事件沿祖先链到根节点 onKeyDown)。
//   4. tab 根页之间 router.replace (栈不加深); "发现"页里 push 详情页,
//      返回走路由栈; badge(未读数) 变化只重渲染对应 tab 项。
//   5. 移动宿主 (android/ios) 上: 底栏自动带手势条安全区 padding、
//      软键盘弹起隐藏底栏、返回键先退详情页 → 再回首个 tab → 再放行退出。
//      这些路径需要真机/宿主上报, 桌面上只验证布局分派。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";
import { createRouter, RouterView, useRoute, useRouteState } from "gx/router";
import { AppShell, createTabs } from "./ui/shell.js";
import { icons } from "./ui/icons.js";

// badge 数据源: signal 驱动, 变化只重渲染"发现"那一项的徽标
const [unread, setUnread] = createSignal(3);

// ---- tab 根页 (全部 keepAlive: 来回切状态原样) ----

function HomePage() {
  const r = useRoute();
  return (
    <column gap={8} padding={16}>
      <text font={18}>首页</text>
      <text font={13} color="#5a6470">{() => "route: " + r().path}</text>
      <rect width={280} height={80} radius={10} background="#e8f0fe" />
      <text font={12} color="#5a6470">拖窄窗口 (< 720) 观察自动切到底部 TabBar</text>
    </column>
  );
}

function DiscoverPage() {
  const r = useRoute();
  return (
    <column gap={8} padding={16}>
      <text font={18}>发现</text>
      <text font={13} color="#5a6470">{() => "route: " + r().path + "  未读: " + unread()}</text>
      <row gap={8}>
        <button onClick={() => setUnread(0)}>清除未读 (badge 归零)</button>
        <button onClick={() => setUnread(unread() + 1)}>未读 +1</button>
      </row>
      <button onClick={() => globalThis.__goxDemoRouter.push("/detail/7")}>
        打开详情页 (push, 返回键/返回按钮可回)
      </button>
    </column>
  );
}

function MinePage() {
  const st = useRouteState();
  const n = (st.get("renders", 0) || 0) + 1;
  st.set("renders", n);
  return (
    <column gap={8} padding={16}>
      <text font={18}>我的</text>
      <text font={13} color="#5a6470">{() => "本页体重跑了 " + n + " 次 (keepAlive 时恒为 1)"}</text>
    </column>
  );
}

function SettingsPage() {
  return (
    <column gap={8} padding={16}>
      <text font={18}>设置 (低频项: 桌面侧栏沉底, 移动端仍在底栏)</text>
    </column>
  );
}

// ---- tab 内详情页 (push 进入, 不开 keepAlive) ----

function DetailPage(props) {
  const router = globalThis.__goxDemoRouter;
  return (
    <column gap={8} padding={16}>
      <text font={18}>{() => "详情 id=" + props.param.id}</text>
      <button onClick={() => router.back()}>返回 (router.back)</button>
    </column>
  );
}

// ---- tabs 声明 + 路由表 ----

const tabs = createTabs([
  { path: "/home", title: "首页", icon: icons.home },
  { path: "/discover", title: "发现", icon: icons.chat, badge: () => unread() },
  { path: "/mine", title: "我的", icon: icons.user },
  { path: "/settings", title: "设置", icon: icons.gear, footer: true },
]);

const router = createRouter({
  routes: [
    { path: "/home", name: "home", component: HomePage, keepAlive: true },
    { path: "/discover", name: "discover", component: DiscoverPage, keepAlive: true },
    { path: "/mine", name: "mine", component: MinePage, keepAlive: true },
    { path: "/settings", name: "settings", component: SettingsPage, keepAlive: true },
    { path: "/detail/:id", name: "detail", component: DetailPage },
    { path: "*", name: "nf", component: HomePage },
  ],
  initial: "/home",
});

// Demo 页面里通过全局引用拿 router (DemoPage 不想层层传 props; 真实应用
// 建议在页面组件里 useRoute()/把 router 作为模块顶层常量)。
globalThis.__goxDemoRouter = router;

render(
  <window title="TabBar demo" width={960} height={640}>
    <AppShell
      tabs={tabs}
      router={router}
      breakpoint={720}
      content={() => <RouterView />}
    />
  </window>
);
