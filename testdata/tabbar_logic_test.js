// ===== TabBar 组件库纯逻辑验证 (无窗口, 不开 GUI) =====
// 运行: go run . testdata/tabbar_logic_test.js
// 覆盖: badge 格式化 / DPI 换算 / 断点分派 / createTabs 工厂 / 图标注册表。
// GUI 形态 (布局、transition、keep-alive) 需要窗口环境, 由 tabbar_demo.js 人工验收。
import { formatBadge } from "./ui/tabbar.js";
import {
  DEFAULT_BREAKPOINT, toLogical, resolveNavMode, createTabs,
} from "./ui/shell.js";
import { icons, ICON_KEYS } from "./ui/icons.js";

let failures = 0;
function check(name, cond) {
  if (cond) { console.log("PASS  " + name); }
  else { console.log("FAIL  " + name); failures++; }
}

// ---- badge (Phase 2.4) ----
check("formatBadge(0) → 空串", formatBadge(0) === "");
check("formatBadge(5) → \"5\"", formatBadge(5) === "5");
check("formatBadge(99) → \"99\"", formatBadge(99) === "99");
check("formatBadge(100) → \"99+\"", formatBadge(100) === "99+");
check("formatBadge(999) → \"99+\"", formatBadge(999) === "99+");
check("formatBadge(-3)/NaN → 空串", formatBadge(-3) === "" && formatBadge("x") === "");

// ---- DPI 换算 (风险提示: onResize 是物理像素) ----
check("toLogical(1440, scale=2) → 720", toLogical(1440, 2) === 720);
check("toLogical scale 非法按 1", toLogical(720, 0) === 720 && toLogical(720, -1) === 720);
check("toLogical(720, 1) → 720", toLogical(720, 1) === 720);

// ---- 分派规则 (缺省断点 720, 宽 → sidenav / 窄 → tabbar) ----
check("断点缺省 720", DEFAULT_BREAKPOINT === 720);
check("宽 720 → sidenav (≥)", resolveNavMode({ width: 720, breakpoint: 720 }) === "sidenav");
check("窄 719 → tabbar (<)", resolveNavMode({ width: 719, breakpoint: 720 }) === "tabbar");
check("缺省断点生效", resolveNavMode({ width: 720 }) === "sidenav" && resolveNavMode({ width: 600 }) === "tabbar");
check("断点可配置", resolveNavMode({ width: 600, breakpoint: 480 }) === "sidenav");

// ---- createTabs 工厂 (与 gx/router path 绑定) ----
const t = createTabs([
  { path: "/home", title: "首页", icon: icons.home },
  { path: "/discover", title: "发现", icon: icons.chat, badge: () => 3 },
  { path: "/mine", title: "我的", icon: icons.user },
  { path: "/settings", title: "设置", icon: icons.gear, footer: true },
]);
check("createTabs 数量与 index", t.tabs.length === 4 && t.tabs[1].index === 1);
check("createTabs key = path", t.tabs[2].key === "/mine" && t.tabs[0].key === "/home");
check("createTabs 字段透传", t.tabs[1].title === "发现" && typeof t.tabs[1].badge === "function" && t.tabs[1].icon === icons.chat);
check("createTabs footer 拆分", t.main.length === 3 && t.footer.length === 1 && t.footer[0].path === "/settings");
check("createTabs indexOf", t.indexOf("/mine") === 2 && t.indexOf("/nope") === -1);

// ---- 图标注册表 (Phase 4) ----
check("图标共 12 个", ICON_KEYS.length === 12);
check("图标都是可调用函数", ICON_KEYS.every((k) => typeof icons[k] === "function"));
check("必需图标齐全", ["home", "search", "user", "gear", "bell", "chat", "folder", "calendar", "heart", "plus", "check", "arrow-left"].every((k) => typeof icons[k] === "function"));

console.log(failures === 0 ? "ALL PASS" : failures + " FAILURES");
