// 应用状态：路由 + 换屏日志 + 窗口列表。signal 用索引取值（引擎纪律）。
import { createSignal } from "gox";
import { createRouter } from "gx/router";
import { windows, onWindowDisplayChange } from "gx/screen";
import { h } from "gox";
import { useRouteState } from "gx/router";
import { colors, font } from "./theme.js";
import { winListText, continuityText } from "./lib/geo.js";

// ===== 路由：首页 + 草稿页（草稿把一句话写进 route.state，随栈搬迁） =====

const HomePage = () => {
  return (
    <column gap={4}>
      <text font={font.md} color={colors.text}>首页</text>
      <text font={font.sm} color={colors.muted}>点 "进草稿页" 写点状态，再试接续</text>
    </column>
  );
}

const DraftPage = () => {
  const st = useRouteState();
  if (!st.has("note")) st.set("note", "在 home 窗口写下的草稿");
  return (
    <column gap={4}>
      <text font={font.md} color={colors.text}>草稿页</text>
      <text font={font.sm} color={colors.muted}>{() => "route.state.note = " + st.get("note")}</text>
    </column>
  );
}

const router = createRouter({
  routes: [
    { path: "/", name: "home", component: HomePage },
    { path: "/draft", name: "draft", component: DraftPage, keepAlive: true },
  ],
  initial: "/",
});

// ===== 换屏日志（两窗口共享这份模块级 signal） =====

const logP = createSignal([]);
const log = logP[0];

onWindowDisplayChange((e) => {
  logP[1](log().concat(["win " + e.windowId + ": " + e.fromDisplay + " -> " + e.toDisplay]));
});

// ===== 窗口列表 / 接续体检（手动刷新） =====

const winListP = createSignal("(点 刷新)");
const contP = createSignal("(点 刷新)");
const winList = winListP[0];
const cont = contP[0];

const refresh = () => {
  winListP[1](winListText(windows()));
  contP[1](continuityText(router.continuity()));
}

export { router, log, winList, cont, refresh };
