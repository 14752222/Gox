// 根组件：路由表 + 页签 + 页面出口。
//
// 页签切换用内置路由模块 `gx/router`（内置模块，不是手写 signal 切页）：页面由
// **路径**标识，于是参数路由 / 守卫 / 历史栈 / 懒加载这些都能直接用上。
//
// 页面切换本身仍是内核既有的"函数子节点 + keep-alive"机制，路由只是把它收进一张
// 路由表：`keepAlive: true` 的页面离开时只把子树摘出布局流（节点与状态都保活），
// 所以在计数器页签调过的值、在输入框里打的字，切走再切回来都还在。
// 想要"每次进入都全新构建"（对标 v-if 那一档），去掉那一条的 keepAlive 即可。
import { h, createRouter, RouterView, RouterLink } from "gox";
import { colors, space, font } from "./theme.js";
import { Counter } from "./components/counter.js";
import { TodoList } from "./components/todo-list.js";
import { StatusBar } from "./components/status-bar.js";

// 页签就是路由表的一项：`path` 是路径，`page` 是页面组件。
const TABS = [
  { path: "/", label: "计数器", page: Counter },
  { path: "/todos", label: "待办列表", page: TodoList },
  { path: "/status", label: "状态", page: StatusBar },
];

const router = createRouter({
  routes: TABS.map((t) => ({ path: t.path, component: t.page, keepAlive: true }))
    // 兜底：不认识的路径回首页（redirect 的记录不需要 component）
    .concat([{ path: "*", redirect: "/" }]),
  initial: "/",
});

// 当前路径的**响应式读取**：currentRoute() 是取值函数（signal 语义），不是属性 ——
// 放进函数 prop / 函数子节点里就会跟着路由变。
//
// 单窗口应用在根级读它是安全的；多窗口下窗口根上读到的是"最近用到的那个会话"，
// 那时该在页面体里用 useRoute()（见 docs/gui-router.md §8.1 与 §11）。
const here = () => router.currentRoute().path;

// 页签 = RouterLink。它自带导航语义（点击 + 回车/空格），关键在 `activeBackground`：
// 命中当前路由时背景换成高亮色 —— 这是一次"函数 prop 即响应式"的内置应用，所以
// 传的是**颜色值**（路由器内部会包成取值函数），而下面 text 的 color 要自己传函数。
//
// ⚠️ 内边距写在里面的 <row> 上，不要写在 RouterLink 自己身上：RouterLink 是个
// `view`（布局透明的容器），**只有一个子节点时子节点直接占满它的盒子**，于是 view
// 自己的 padding 既不进固有尺寸也不缩子节点 —— 写了等于没写（不报错，只是没效果）。
// 换成一个 row 承载 padding 就正常：盒子被撑开，RouterLink 的背景正好盖住它。
const TabButton = (p) => (
  <RouterLink to={p.path} background={colors.panel} activeBackground={colors.accent}>
    <row padding={5}>
      <text color={() => (here() === p.path ? colors.accentFg : colors.text)}>{p.label}</text>
    </row>
  </RouterLink>
);

export function App() {
  return (
    <column gap={space.md} padding={space.lg}>
      <text font={font.xl} color={colors.text}>__PROJECT_TITLE__</text>

      <row gap={space.sm} alignItems="center">
        {TABS.map((t) => <TabButton label={t.label} path={t.path} />)}
      </row>

      <separator />

      {/* 当前页面在这里出现。RouterView 同样是布局透明的容器，自己不多占一个盒子。 */}
      <RouterView />

      <spacer flexGrow={1} />
      <separator />

      {/* show 指令（元素级指令，对标 v-show）：隐藏只是把子树摘出布局流 —— keep-alive，
          隐藏不销毁。这里用它做一条"只在非首页出现"的提示。
          ⚠️ children 位置如果要放组件，写 {Comp} 而不是 <Comp/>：JSX 里大写标签是
          **当场调用**，写在指令容器里等于"脚本求值期就跑了一遍组件体"。 */}
      <view show={() => here() !== "/"}>
        <text font={font.sm} color={colors.muted}>
          （这条只在非首页出现 —— show 是 keep-alive 的显隐，切回首页只是摘出布局流）
        </text>
      </view>

      <text font={font.sm} color={colors.muted}>
        JSX + signal + 路由 · 改 src/ 下的文件后重跑即可
      </text>
    </column>
  );
}
