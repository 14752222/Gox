// M8 演示: 多屏协同 —— 跨屏窗口几何 + 应用接续。
// 运行: ./gox testdata/multiscreen_demo.js
//
// 现象:
//   1. 打开两个窗口 "home" 与 "road", 各有自己的导航栈 (gx/router 按窗口作用域分离)。
//   2. 每个窗口能读自己的几何 (句柄 position/bounds): 点 "读几何" 显示
//      工作区相对位置 / 客户区尺寸 / 所在显示器 / 缩放。
//      - "居中本窗口" 走 center();
//      - "跨屏试挪" 走 moveTo(1300, 120): 当前屏工作区外的坐标会落到相邻屏。
//   3. 用鼠标把任一窗口拖到另一块屏: onWindowDisplayChange 会记一行
//      "win N: <from> -> <to>" (两个窗口都能看到, 因为事件按 windowId 区分)。
//   4. 在 home 里点 "进 draft" 并写下状态, 再点 "接续到 road": draft 整条栈
//      (含 route.state 里的 note) 搬到 road, home 复位回首页;
//      "复制到 road" 用 { keepSource: true } 保留源 (road 拿克隆, 两边各自独立)。
//
// v1 边界 (完整说明见 docs/multi-window.md §8):
//   - **跨设备接续 (手机 -> 桌面) 不做**: 需要设备配对 + 传输通道, 超出本里程碑。
//     这里演示的同机跨窗口接续与跨设备**语义完全一致** (搬迁 + 源复位), 差的只是
//     一层传输层; continuity() 就是给未来的接续协调器做只读体检用的。
//   - render() 配置暂不解析 x/y/display: 初始按屏放置只有 Go 嵌入侧可用
//     (WindowConfig.Display), JS 侧用 moveTo 跨屏。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";
import { createRouter, RouterView, RouterLink, useRouteState } from "gx/router";
import { windows, onWindowDisplayChange } from "gx/screen";

// ===== 路由: 两个页面, draft 页把一句话写进 route.state =====

function HomePage() {
  return (
    <column gap={4}>
      <text font={14}>home page</text>
      <text font={12} color="#667">点 "进 draft" 进入草稿页</text>
    </column>
  );
}

function DraftPage() {
  const st = useRouteState();
  if (!st.has("note")) st.set("note", "typed on home");
  return (
    <column gap={4}>
      <text font={14}>draft page</text>
      <text font={12} color="#667">{() => "note = " + st.get("note")}</text>
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

// ===== 跨屏事件日志 + 窗口列表 (两个窗口都能看到) =====

const [log, setLog] = createSignal([]);
onWindowDisplayChange((e) => {
  setLog(log().concat([`win ${e.windowId}: ${e.fromDisplay} -> ${e.toDisplay}`]));
});

const [winList, setWinList] = createSignal("(点 refresh 刷新)");
const refreshWindows = () => {
  setWinList(windows().map((w) => `${w.id}:${w.title}@${w.displayId}`).join("   |   "));
};

// ===== 两个窗口 (句柄在 render 之后才拿得到, 所以用 let 占位) =====

let homeHandle = null;
let roadHandle = null;

const [homeGeo, setHomeGeo] = createSignal("(点 读几何 获取)");
const [roadGeo, setRoadGeo] = createSignal("(点 读几何 获取)");

const readGeo = (w) => {
  const p = w.position();
  const b = w.bounds();
  return `pos=(${p.x},${p.y})  size=${b.width}x${b.height}  display=${b.displayId}  scale=${b.scale}`;
};

const LogLine = () => (
  <text font={11} color="#557">
    {() => (log().length ? log().join("   |   ") : "(把窗口拖到另一块屏, 这里会出现换屏记录)")}
  </text>
);

const WinListLine = () => (
  <row gap={8}>
    <button onClick={refreshWindows}>refresh</button>
    <text font={11} color="#557">{() => winList()}</text>
  </row>
);

homeHandle = render(
  <window title="home" width={460} height={360}>
    <column gap={8} padding={12}>
      <text font={15}>home window</text>
      <text font={11} color="#667">{() => homeGeo()}</text>
      <row gap={8}>
        <button onClick={() => setHomeGeo(readGeo(homeHandle))}>读几何</button>
        <button onClick={() => homeHandle.center()}>居中本窗口</button>
        <button onClick={() => homeHandle.moveTo(1300, 120)}>跨屏试挪</button>
      </row>

      <row gap={8}>
        <RouterLink to="/draft"><text font={13}>进 draft</text></RouterLink>
        <button onClick={() => router.handoff(homeHandle, roadHandle)}>接续到 road</button>
        <button onClick={() => router.handoff(homeHandle, roadHandle, { keepSource: true })}>
          复制到 road
        </button>
      </row>

      <RouterView />
      <WinListLine />
      <LogLine />
    </column>
  </window>
);

roadHandle = render(
  <window title="road" width={460} height={360}>
    <column gap={8} padding={12}>
      <text font={15}>road window</text>
      <text font={11} color="#667">{() => roadGeo()}</text>
      <row gap={8}>
        <button onClick={() => setRoadGeo(readGeo(roadHandle))}>读几何</button>
        <button onClick={() => roadHandle.center()}>居中本窗口</button>
      </row>

      <text font={12} color="#889">
        home 点 "接续到 road" 后, 这条导航栈会搬到这里
      </text>

      <RouterView />
      <WinListLine />
      <LogLine />
    </column>
  </window>
);

globalThis.winHome = homeHandle;
globalThis.winRoad = roadHandle;
globalThis.gxRouter = router;
globalThis.navLog = log;
void 0;
