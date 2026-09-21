// gx/router 演示: 路由注册与匹配 / 参数路由 / 懒加载 / 三级守卫 / 历史栈。
// 运行: go run . testdata/router_demo.js
//
// 现象:
//   1. 顶部实时显示当前路由; 中间一排 RouterLink 可点击切页。
//   2. 点 "detail 7" → 懒加载 (先 loading 再出内容), 页面显示 id=7 与访问次数。
//   3. 点 "guard" → 被 beforeEnter 拦下, 路由**不变** (按钮点了没反应是对的)。
//   4. back 按钮回上一页; keepAlive 的 list 页回来时状态原样。
//
// 与 routing_demo.js (路由 A 模式) 的对照: 那个演示用"一个 signal + 三元"手写
// 切页; 这里同一件事由路径词汇 (:id) / 路由表 / 守卫链 / 历史栈表达。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";
import {
  createRouter, RouterView, RouterLink, lazy, useRoute, useRouteState,
} from "gx/router";

const log = [];
globalThis.enterLog = [];
globalThis.leaveLog = [];

// allowGuard: 由测试/演示切换, 用来观察"全局守卫拦截"与"放行"两种结果。
const [allowGuard, setAllowGuard] = createSignal(true);
const [listDraft, setListDraft] = createSignal("");

// ---- 页面组件 (普通函数: 大写标签就是当场调用) ----

function HomePage() {
  const route = useRoute();
  return (
    <column gap={6}>
      <text font={15}>home page</text>
      <text font={12} color="#667">{() => "useRoute in page: " + route().path}</text>
    </column>
  );
}

// ListPage 演示两件事:
//   - keepAlive: true —— 离开再回来, 输入框里的草稿还在
//   - useRouteState —— 状态记在历史栈项上 (与 keepAlive 正交的第二档保持)
function ListPage() {
  const route = useRoute();
  const st = useRouteState();
  const n = (st.get("renders", 0) || 0) + 1;
  st.set("renders", n);
  return (
    <column gap={6}>
      <text font={15}>list page</text>
      <input width={200} value={() => listDraft()} onInput={(e) => setListDraft(e.value)} />
      <text font={12} color="#667">{() => "path: " + route().path + " renders=" + n}</text>
    </column>
  );
}

function GuardPage() {
  return <text font={15}>guard page (should never be reachable)</text>;
}

function NotFoundPage() {
  const route = useRoute();
  return (
    <column gap={6}>
      <text font={15}>not found</text>
      <text font={12} color="#667">{() => "path: " + route().path}</text>
    </column>
  );
}

// ---- 路由表 ----
//   "/", "/list", "/guard" 是同步组件; "/detail/:id" 是懒加载;
//   "/blocked" 永远被全局守卫拦下;  "*" 是兜底。
const router = createRouter({
  routes: [
    { path: "/", name: "home", component: HomePage },
    { path: "/list", name: "list", component: ListPage, keepAlive: true },
    {
      path: "/detail/:id",
      name: "detail",
      component: lazy(() => import("./router_page_detail.js")),
    },
    { path: "/guard", name: "guard", component: GuardPage, beforeEnter: guardByRule },
    { path: "/blocked", name: "blocked", component: NotFoundPage },
    { path: "*", name: "nf", component: NotFoundPage },
  ],
  initial: "/",
});

function guardByRule(to, from) {
  log.push("beforeEnter:" + to.path);
  return false; // 路由级守卫: 永远不放行 (演示"进入拦截")
}

// 全局守卫: 记录每一次导航, 并按 allowGuard 拦下 /blocked。
router.beforeEach((to, from) => {
  log.push("g:before:" + to.path + "<-" + (from ? from.path : "-"));
  if (to.path === "/blocked" && !allowGuard()) return false;
});
router.afterEach((to, from) => {
  log.push("g:after:" + to.path);
});
router.onError((e) => {
  log.push("g:error:" + String(e && e.message ? e.message : e));
});

render(
  <window title="router demo" width={480} height={360}>
    <column gap={8} padding={12}>
      <text font={13} color="#88a">{() => "route: " + router.currentRoute().path}</text>

      <row gap={10}>
        <RouterLink to="/" activeBackground="#dde8ff"><text font={13}>home</text></RouterLink>
        <RouterLink to="/list" activeBackground="#dde8ff"><text font={13}>list</text></RouterLink>
        <RouterLink to={{ name: "detail", params: { id: 7 } }} activeBackground="#dde8ff">
          <text font={13}>detail 7</text>
        </RouterLink>
        <RouterLink to="/guard"><text font={13}>guard</text></RouterLink>
        <RouterLink to="/deep/unknown"><text font={13}>missing</text></RouterLink>
      </row>

      <RouterView loading={<text font={12} color="#a70">loading page...</text>} />

      <row gap={8}>
        <button onClick={() => router.back()}>back</button>
        <button onClick={() => router.push("/list")}>to list</button>
        <button onClick={() => router.push("/detail/9")}>detail 9</button>
      </row>
    </column>
  </window>
);

// ---- 测试/调试钩子 (真机上没有这些也能跑) ----
//
// 命名纪律: 钩子名**不能与顶层 const 同名**。本引擎里 `globalThis.x = x` 的
// 右侧会解析到刚建好的同名全局属性 (而不是外层那个 const), 于是
// `globalThis.setAllowGuard = (v) => setAllowGuard(v)` 变成"箭头调自己" ——
// 症状是栈溢出或静默无效。所以钩子一律另起名字。
globalThis.gxRouter = router;
globalThis.navLog = log;
globalThis.setAllowGuardHook = (v) => setAllowGuard(v);
globalThis.pushRoute = (p) => router.push(p);
globalThis.replaceRoute = (p) => router.replace(p);
globalThis.navBack = () => router.back();
globalThis.routePath = () => router.currentRoute().path;
globalThis.historyPaths = () => router.history().map((e) => e.path);
