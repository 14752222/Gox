// ===== AppShell —— PC / 移动自适应导航壳 (Phase 1 分派器 + Phase 2/3 差异) =====
//
// 一套声明式 API, 两端形态:
//   宽 (逻辑宽 ≥ breakpoint, 缺省 720)  → row { SideNav | 内容区 }
//   窄                                  → column { 内容区 | TabBar }
// 业务只声明 tabs, 分派由这里统一承担 (分派规则是纯函数 resolveNavMode, 可单测)。
//
// 平台差异全在本文件收口:
//   - DPI: onResize 给的是**物理像素**, 与断点比较前先除以 windowInfo().scale
//     (gx/screen), 否则高 DPI 设备会错判档位。
//   - 底部安全区: TabBar 内部用 useInsets().bottom 拼 paddingBottom (桌面恒 0)。
//   - 软键盘: keyboard > 0 → 底栏 show={false} 隐藏 (keep-alive), 收起自动恢复;
//     键盘 insets 与底部 insets 不在这里相加 —— 底栏隐藏时二者不会同时生效。
//   - Android 返回键: onBackPress 返回真值 = 已消费。栈里有上一页 (tab 内详情页)
//     → router.back(); 已在 tab 根 → 切回第一个 tab; 再按才放行退出。
//     注意 gx/router 没有 canBack(), 用 router.index(scope) > 0 判断。
//   - 快捷键: Ctrl+1..9 切 tab (根节点 onKeyDown; 键盘事件沿祖先链派发,
//     根是所有节点的祖先)。要"免焦点"的事件泵级匹配, 用 menuitem shortcut
//     (见 docs/gui-tabbar.md 坑位清单)。
//   - 折叠记忆: SideNav 折叠态持久化到 gx/storage (键 gox.sidenav.collapsed)。
//   - 多窗口: active 信号是 AppShell 实例的局部状态 —— 每个窗口各自跑一遍
//     组件函数, 天然不共享; 路由栈隔离沿用 gx/router 的 scope 概念
//     (render 返回的句柄可作 win prop 传入以精确指认作用域)。
//
// 路由联动 (已拍板): tab 根页之间 router.replace (防栈无限深), tab 内详情页
// 才 push; tab 根页开 keepAlive。
import { createSignal, createMemo } from "gx/solid";
import { useViewport } from "gx/viewport";
import { onBackPress } from "gx/app";
import { deviceInfo } from "gx/device";
import { useWindowInfo } from "gx/screen";
import { getStorage, setStorage } from "gx/storage";
import { TabBar } from "./tabbar.js";
import { SideNav } from "./sidenav.js";

export const DEFAULT_BREAKPOINT = 720;

const MOBILE_PLATFORMS = ["android", "ios", "harmony"];
const COLLAPSE_KEY = "gox.sidenav.collapsed";

// ---- 纯函数 (tabbar_logic_test.js 覆盖) ----

// 物理像素 → 逻辑像素 (scale 缺省/非法按 1)。
export function toLogical(px, scale) {
  const s = scale > 0 ? scale : 1;
  return px / s;
}

// 分派规则 (单一函数): 逻辑宽 ≥ breakpoint → "sidenav", 否则 → "tabbar"。
// 移动宿主的 widthTier 修正也走同一条宽度比较 (宿主上报的窗口宽已经是
// 分屏/折叠后的真相), 不引入第二套档位词汇。
export function resolveNavMode(opts) {
  const bp = opts && opts.breakpoint != null ? opts.breakpoint : DEFAULT_BREAKPOINT;
  const w = opts ? opts.width : 0;
  return w >= bp ? "sidenav" : "tabbar";
}

// createTabs 工厂: tab 声明 → 带 key/index 的项, 与 gx/router path 绑定。
//   { path, title, icon, badge?, footer? }
//   footer: true 的项在桌面 SideNav 里沉底 (spacer 之后), 移动端照常进底栏。
// 返回 { tabs, main, footer, indexOf(path) } —— 也接受裸数组 (AppShell 会归一化)。
export function createTabs(defs) {
  const tabs = (defs || []).map((d, i) => ({
    key: d.path || String(i),
    index: i,
    path: d.path || "/",
    title: d.title || d.path,
    icon: d.icon,
    badge: d.badge, // 可选: () => number (signal 驱动)
    footer: !!d.footer,
  }));
  return {
    tabs,
    main: tabs.filter((t) => !t.footer),
    footer: tabs.filter((t) => t.footer),
    indexOf: (path) => {
      for (let i = 0; i < tabs.length; i++) {
        if (tabs[i].path === path) return i;
      }
      return -1;
    },
  };
}

function normalizeTabs(x) {
  if (Array.isArray(x)) return createTabs(x);
  return x || createTabs([]);
}

// ---- AppShell ----
// 用法:
//   <AppShell tabs={tabs} router={router} breakpoint={720}
//             content={() => <RouterView />} />
// content 传**函数**: 形态切换 (窄 ↔ 宽) 时每次求值都新建内容元素,
// 直接传 JSX 元素会在切换时复用同一节点对象 (引擎里一个节点只有一个 Parent)。
export function AppShell(p, ...kids) {
  const nav = normalizeTabs(p.tabs);
  const router = p.router;
  const winArg = p.win; // 可选: 窗口句柄或 scope 名 (多窗口精确指认)
  const breakpoint = p.breakpoint != null ? p.breakpoint : DEFAULT_BREAKPOINT;
  const isMobile = MOBILE_PLATFORMS.indexOf(deviceInfo().platform) !== -1;
  const contentOf =
    typeof p.content === "function"
      ? p.content
      : function () {
          return kids.length === 1 ? kids[0] : kids;
        };

  // gx/viewport · gx/screen 的 useXxx() 本身就是取值函数:
  // 每次调用 = 订阅一次环境版本号 + 读一次快照, 所以在响应式读取点**现调现读**,
  // 不能把返回值存下来再调用 (返回的是快照对象, 不是 getter)。
  const [resized, setResized] = createSignal(null);
  const [active, setActive] = createSignal(initialIndex());
  const [collapsed, setCollapsed] = createSignal(readCollapsed());

  function initialIndex() {
    if (!router) return 0;
    try {
      const i = nav.indexOf(router.currentRoute(winArg).path);
      return i >= 0 ? i : 0;
    } catch (e) {
      return 0; // 尚无会话时 currentRoute 给 initial 快照, 这里兜底防意外
    }
  }

  // tab 根页之间 replace (不压栈); tab 内详情页由业务自己 push。
  function select(i) {
    const t = nav.tabs[i];
    if (!t) return;
    setActive(i);
    if (router) {
      if (winArg) router.replace(t.path, winArg);
      else router.replace(t.path);
    }
  }

  // 折叠记忆 (gx/storage; 存储损坏按空处理是内核语义, 这里再兜一层)。
  function readCollapsed() {
    try {
      return getStorage(COLLAPSE_KEY) === true;
    } catch (e) {
      return false;
    }
  }
  function toggleCollapsed() {
    const v = !collapsed();
    setCollapsed(v);
    try {
      setStorage(COLLAPSE_KEY, v);
    } catch (e) {}
  }

  // onResize 是**物理像素**: 先除以 DPI 再进断点比较。
  // 初始值用 windowInfo().width (与 onResize 同口径), 首帧即有正确档位。
  const wide = createMemo(() => {
    const r = resized();
    const info = useWindowInfo();
    const scale = info ? info.scale : 1;
    const phys = r ? r.width : info ? info.width : 0;
    return resolveNavMode({ width: toLogical(phys, scale), breakpoint }) === "sidenav";
  });

  function handleResize(e) {
    setResized({ width: e.width, height: e.height });
  }

  // Ctrl+1..9 切 tab (仅桌面形态注册到行为里; 移动端无此概念)。
  function handleKeys(e) {
    if (!e.ctrl || e.alt || e.shift) return;
    const n = Number(e.key);
    if (n >= 1 && n <= Math.min(9, nav.tabs.length)) select(n - 1);
  }

  // Android 返回键 (仅移动平台注册): 返回真值 = 已消费, 宿主不关界面。
  // 栈里有上一页 → back(); 已在 tab 根 → 切回第一个 tab; 都不是 → 放行退出。
  if (isMobile) {
    onBackPress(() => {
      if (router) {
        const idx = router.index(winArg);
        if (idx > 0) {
          if (winArg) router.back(winArg);
          else router.back();
          return true;
        }
      }
      if (active() !== 0) {
        select(0);
        return true;
      }
      return false;
    });
  }

  // 桌面形态: row { SideNav(可折叠) | 内容区 }
  const desktopBody = () => (
    <row width={"100%"} height={"100%"}>
      <SideNav
        items={nav.main}
        footerItems={nav.footer}
        active={active}
        onChange={select}
        collapsed={collapsed}
        onToggleCollapse={toggleCollapsed}
        showToggle={p.collapsible !== false}
      />
      <column flexGrow={1} height={"100%"}>
        {contentOf()}
      </column>
    </row>
  );

  // 移动形态: column { 内容区(占满) | TabBar(键盘弹起隐藏, keep-alive) }
  const mobileBody = () => (
    <column width={"100%"} height={"100%"}>
      <column flexGrow={1}>{contentOf()}</column>
      <view show={() => {
        const v = useViewport();
        return !v || !(v.keyboard > 0);
      }}>
        <TabBar items={nav.tabs} active={active} onChange={select} />
      </view>
    </column>
  );

  // 根节点: onResize 只派发给布局根, 所以根必须稳定存在 ——
  // 形态切换发生在根的**函数子节点**里, 根本身不换。
  return (
    <column width={"100%"} height={"100%"} onResize={handleResize} onKeyDown={handleKeys}>
      {() => (wide() ? desktopBody() : mobileBody())}
    </column>
  );
}
