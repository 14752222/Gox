// ============================================================================
// §4 路由的定义与注册 (gx/router)
//
// 运行:  gox testdata/tutorial_router.js
//
// 现象: 顶部实时显示当前路由; 一排 RouterLink 点着切页; 底部按钮演示
//       后退 / 登录开关。要点全在注释里标了 (路由表字段 / 参数 / 守卫 / 保活)。
//
// 心智模型: 路由是**模块层能力** —— 页面注册与匹配、历史栈、守卫都在 gx/router 里,
// 页面切换仍然走内核既有的"函数子节点 + keep-alive 分支", 没有新的渲染概念。
// 对照 Vue3 Router: createRouter / push / RouterView / RouterLink / useRoute /
// beforeEach / beforeEnter / keepAlive 同名同义, 只是 currentRoute 是**取值函数**
// (router.currentRoute()) 而不是 ref 属性。
// ============================================================================
import { createSignal } from "gox";
import {
  h, render, createRouter, RouterView, RouterLink, useRoute, useRouteState,
} from "gox";

const navLog = [];
const [loggedIn, setLoggedIn] = createSignal(false);

// ---- 4.1 页面就是普通函数组件 (props 收路由参数) ---------------------------

function HomePage() {
  const route = useRoute(); // 取值函数: 页面里读它是订阅, 路径变化自动更新
  return (
    <column gap={6}>
      <text font={15}>home page</text>
      <text font={12} color="#667">{() => "useRoute() in page: " + route().path}</text>
    </column>
  );
}

// ListPage 演示"两档状态保留"里的两档:
//   keepAlive: true  → 离开时子树被摘出而不销毁, 回来原样 (草稿、滚动、局部 signal 都在)
//   useRouteState()  → 把值记在**历史栈项**上 (子树即使重建, 值也还在)
function ListPage() {
  const route = useRoute();
  const st = useRouteState();
  const n = (st.get("renders", 0) || 0) + 1;
  st.set("renders", n);
  return (
    <column gap={6}>
      <text font={15}>list page</text>
      <text font={12} color="#667">{() => "path " + route().path + " · renders=" + n + " (keepAlive 时不增)"}</text>
    </column>
  );
}

// 参数路由: 路径里的 :id 会平铺成 props.param (记录里写 props: true 时也合并进 props)
function DetailPage(props) {
  const route = useRoute();
  return (
    <column gap={6}>
      <text font={15}>{"detail id=" + props.param.id}</text>
      <text font={12} color="#667">{() => "path: " + route().path}</text>
    </column>
  );
}

function AdminPage() {
  return <text font={15}>admin page (只有登录后才进得来)</text>;
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

// ---- 4.2 路由表: 注册就是声明一张数组 --------------------------------------
//
// 字段: { path, name, component, children, meta, redirect, props, beforeEnter,
//         keepAlive, dualPane }
// 匹配优先级**不依赖声明序**: 静态段 > :param > :param? > 通配。
const router = createRouter({
  routes: [
    { path: "/", name: "home", component: HomePage },
    { path: "/list", name: "list", component: ListPage, keepAlive: true },
    { path: "/detail/:id", name: "detail", component: DetailPage, props: true },
    { path: "/admin", name: "admin", component: AdminPage, beforeEnter: requireLogin },
    { path: "/old", name: "legacy", redirect: "/list" }, // 重定向(另一条记录派发)
    { path: "*", name: "nf", component: NotFoundPage }, // 兜底, 写在最后
  ],
  initial: "/", // 没有 URL, "初始路径"由这里给 (要做 deep-link 用 process.argv 传)
});

// ---- 4.3 三级守卫: 全局 / 路由级 / 组件级 ----------------------------------
// 顺序与 vue-router 一致: beforeRouteLeave → beforeEach → beforeEnter →
// beforeRouteUpdate → beforeRouteEnter → 提交(改栈) → afterEach。
// 关键时机: 守卫全部通过之前**不动历史栈** ⇒ 被拦下的导航不留半截状态。
function requireLogin(to, from) {
  if (!loggedIn()) return "/"; // 返回路径 = 重定向 (重新走一遍守卫链, 上限 8 次)
  return true; // 放行
}

router.beforeEach((to, from) => {
  navLog.push("before:" + to.path + "<-" + (from ? from.path : "-"));
  // 返回 false = 中止 (push 的 Promise 以 {ok:false, reason:"aborted"} 解决)
});
router.afterEach((to) => {
  navLog.push("after:" + to.path);
});

// ---- 4.4 挂载: RouterView 是"当前页面的出口" ------------------------------
// 大写标签是**当场调用**, 所以 <RouterView/> / <RouterLink/> 不用注册。
render(
  <window title="router tutorial" width={520} height={400}>
    <column gap={8} padding={12}>
      {/* 窗口根上读 currentRoute() 会落到"最近用到的会话"; 要显示本窗口的路径,
          请在页面体里用 useRoute() (§4 排障表里的头一条)。 */}
      <text font={13} color="#88a">{() => "route: " + router.currentRoute().path}</text>

      <row gap={10}>
        <RouterLink to="/"><text font={13}>home</text></RouterLink>
        <RouterLink to="/list"><text font={13}>list</text></RouterLink>
        {/* to 也可以用命名路由对象: {name, params} */}
        <RouterLink to={{ name: "detail", params: { id: 7 } }}><text font={13}>detail 7</text></RouterLink>
        <RouterLink to="/admin"><text font={13}>admin</text></RouterLink>
        <RouterLink to="/nope/deep"><text font={13}>missing</text></RouterLink>
      </row>

      <separator />

      {/* 页面内容出口。RouterView 认领"它挂在哪个窗口上" ⇒ 每个窗口一套独立导航栈 */}
      <RouterView />

      <spacer flexGrow={1} />
      <row gap={8}>
        <button onClick={() => router.back()}>back</button>
        <button onClick={() => setLoggedIn((v) => !v)}>toggle login</button>
        <text font={12} color="#667">{() => "loggedIn = " + loggedIn()}</text>
      </row>
    </column>
  </window>
);

// ---- 测试/调试钩子 (真机运行不需要) ---------------------------------------
// 命名纪律: `globalThis.x = x` 里右侧会解析到刚建的同名属性 ⇒ 钩子名必须与
// 顶层 const 不同名, 否则会变成"箭头调自己"(栈溢出或静默无效)。
globalThis.gxRouter = router;
globalThis.navLog = navLog;
globalThis.pushRoute = (p) => router.push(p);
globalThis.navBack = () => router.back();
globalThis.routePath = () => router.currentRoute().path;
globalThis.setLoggedInHook = (v) => setLoggedIn(v);
