// gx/router 多窗口 / 多屏 / 折叠屏演示。
// 运行: go run . testdata/router_window_demo.js
//
// 现象:
//   1. 两个窗口共用一张路由表, 但**各有各的导航栈** (在 A 里切页, B 不动)。
//   2. sync([A, B]) 之后 A 的导航会镜像到 B (路径同步, 页面状态各自独立)。
//   3. reportPosture 把这块屏报成"半折" → 当前窗口变成**双栏**: 左栏是上一条
//      (列表), 右栏是当前页 (详情)。
//   4. 折叠前后路由与状态都不丢: note 页回访时读到状态袋里的标记 (页面子树
//      被销毁重建过, 但状态挂在历史栈项上, 因此还在)。
//
// 说明: 桌面没有折叠姿态查询 API (见 docs/gui-router.md 的"折叠屏"), 所以
// 姿态由宿主/模拟器经 reportPosture 上报 —— 这条链路与移动宿主上报完全一致,
// 只是数据来源不同。
import { h, render } from "gx/gfx";
import { createRouter, RouterView, RouterLink, useRoute, useRouteState } from "gx/router";
import { reportPosture, resetDisplays, screenOf, posture, hinge, windowInfo } from "gx/screen";

const events = [];

function HomePage() {
  const route = useRoute();
  return (
    <column gap={6}>
      <text font={15}>home page</text>
      <text font={12} color="#667">{() => "path: " + route().path}</text>
    </column>
  );
}

// ListPage: keepAlive —— 被摘出去但活着, 回来时子树原样 (草稿/滚动都在)。
function ListPage() {
  const route = useRoute();
  const st = useRouteState();
  const n = (st.get("renders", 0) || 0) + 1;
  st.set("renders", n);
  return (
    <column gap={6}>
      <text font={15}>list page</text>
      <text font={12} color="#667">{() => "path: " + route().path + " renders=" + n}</text>
    </column>
  );
}

// NotePage: **不保活** —— 离开即销毁。它靠状态袋 prove 一件事:
// 子树重建之后, 挂在历史栈项上的状态还在。
function NotePage(props) {
  const route = useRoute();
  const st = useRouteState();
  const kept = st.has("seen");
  st.set("seen", true);
  return (
    <column gap={6}>
      <text font={15}>{"note " + (kept ? "kept" : "fresh")}</text>
      <text font={12} color="#667">{() => "id=" + props.param.id + " path: " + route().path}</text>
    </column>
  );
}

function NotFoundPage() {
  return <text font={15}>not found</text>;
}

const router = createRouter({
  routes: [
    { path: "/", name: "home", component: HomePage },
    { path: "/list", name: "list", component: ListPage, keepAlive: true },
    { path: "/note/:id", name: "note", component: NotePage },
    { path: "*", name: "nf", component: NotFoundPage },
  ],
  initial: "/",
});



// 两个窗口: A 用自动作用域 (win:N), B 用显式作用域名 "preview"。
const wa = render(
  <window title="router A" width={420} height={300}>
    <column gap={6} padding={10}>
      <row gap={10}>
        <RouterLink to="/list"><text font={13}>A:list</text></RouterLink>
        <RouterLink to={{ name: "note", params: { id: 1 } }}><text font={13}>A:note</text></RouterLink>
      </row>
      <RouterView />
    </column>
  </window>
);

const wb = render(
  <window title="router B" width={420} height={300}>
    <column gap={6} padding={10}>
      <row gap={10}>
        <RouterLink to="/list"><text font={13}>B:list</text></RouterLink>
      </row>
      <RouterView scope="preview" />
    </column>
  </window>
);

// ---- 测试/调试钩子 (钩子名不与顶层 const 同名, 见 router_demo.js 的说明) ----
globalThis.gxRouter = router;
globalThis.navEvents = events;
globalThis.winA = wa;
globalThis.winB = wb;
globalThis.pushA = (p) => router.push(p, wa);
globalThis.pushB = (p) => router.push(p, wb);
globalThis.backA = () => router.back(wa);
// 注意作用域的命名规则: **窗口句柄代表它的"自动作用域"** (win:N)。B 的
// RouterView 声明了 scope="preview", 那些视图挂在 "preview" 上 —— 所以 B 要
// 用作用域名来指认。两种写法并存正是这个 API 的意图:
//   句柄   → "这个窗口" (自动作用域, 零配置)
//   字符串 → "这个具名作用域" (一个窗口里可以有多个, 也可以跨窗口共用)
globalThis.syncAB = (mode) => router.sync([wa, "preview"], { mode: mode });
globalThis.scopeDump = () => router.scopes().map((s) => s.scope + ":" + s.path).join(",");
// 姿态上报按"窗口所在的那块屏"来 (screenOf(win).id): 后端负责枚举与"窗口在
// 哪块屏上", 宿主只负责姿态 —— 两者各说各知道的那一半。
globalThis.fold = () => reportPosture({
  display: screenOf(wa).id,
  foldable: true,
  width: screenOf(wa).width,
  height: screenOf(wa).height,
  posture: "half-open",
  hinge: { x: 700, y: 0, w: 24, h: 1000, orientation: "vertical" },
});
globalThis.unfold = () => reportPosture({ display: screenOf(wa).id, posture: "flat" });
// 姿态是**按屏**的: A 在 panel-0, B 在 panel-1 —— 折叠 A 的屏幕不该动 B。
globalThis.postureA = () => posture(wa);
globalThis.postureB = () => posture(wb);
globalThis.hingeWidth = () => (hinge() ? hinge().width : 0);
globalThis.windowInfoNow = () => windowInfo().screenWidth + "x" + windowInfo().screenHeight;
globalThis.resetScreens = () => resetDisplays();
