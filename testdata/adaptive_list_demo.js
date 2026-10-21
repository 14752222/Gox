// ============================================================
// README 首屏示例：单双栏自适应列表页（看板单 r1yfFa / N2）
// 运行: gox testdata/adaptive_list_demo.js
//
// 现象：窗口宽度 < 600dp 时是**单栏**（列表 + 选中项就地展开）；
// 拖宽到 >= 600dp 自动变**双栏**（左侧列表 + 右侧详情并排）。
// 桌面拖动窗口边缘即可；顶部三个按钮直接跳到 480 / 700 / 1000dp。
// 换档只重算布局，不重置选中项 —— 断点就是一次普通响应式读数。
//
// ------------------------------------------------------------
// 【坑 1】纯读数会短路掉订阅（首屏示例里最容易踩的一条）
// ------------------------------------------------------------
// breakpoint() / posture() / hasFold() 这种**纯读数不订阅**；只有
// useBreakpoint() / useLayoutMode() 这类两段式返回的取值函数才订阅。
// 把纯读数写在三元的第一操作数、或 && 的左侧，被短路的那一支**一次都没读**
// ⇒ 零依赖 ⇒ 之后宽度变了这一支永不重跑，而且**没有任何警告**：
//
//   错：const cols = breakpoint() === "sm" ? 1 : 2;   // 首帧定死后不更新
//   错：const wide = isWide && breakpoint() !== "sm"; // isWide 为假时右侧不读
//   对：const bp = useBreakpoint();  // 每次求值里先**无条件** bp() 一次
//
// 本文件的 layout() 就是「对」的写法：函数体第一行无条件读一次 bp()。
//
// ------------------------------------------------------------
// 【坑 2】坐标单位是两套
// ------------------------------------------------------------
// reservedRegions() 的 x/y/width/height 是**设备像素**（折痕、挖孔）；
// safeAreaStyle() 的产出与脚本显式写的尺寸属性都是 **dp**（内核每帧乘
// Display.Scale，见 gfx/density.go）。折痕避让时不要把前者直接喂给布局
// 属性 —— 先除 pixelRatio：
//
//   错：<column paddingLeft={regions()[0].width}>       // 像素当 dp 用，高分屏偏大
//   对：<column paddingLeft={regions()[0].width / pixelRatio()}>
//
// ------------------------------------------------------------
// 【措辞】所谓「不用写 if」
// ------------------------------------------------------------
// 严格讲不成立：下面的 <Switch><Match when={...}> 本身就是条件表达。
// 准确的收益是：**布局分支由断点名声明**（一张 sm/md/lg/xl 的表），
// 而不是把同一个宽度判断散落在 N 处 if 里 —— 加一档只改表，不改 N 处判断。
// ============================================================

import { h, render } from "gx/gfx";
import { createSignal } from "gx/solid";
import { useBreakpoint, breakpoint, matchBreakpoint, breakpoints } from "gx/viewport";
import { Switch, Match } from "gx/view";

const bp = useBreakpoint();                 // () => "sm" | "md" | "lg" | "xl"（订阅型）
const [selected, setSelected] = createSignal(0);

// 一张表声明四档布局：加一档只改这里，不动下面的 JSX。
const layout = () => {
  bp();   // ← 无条件读一次 = 订阅登记。删掉这一行，整页不再自适应（无警告）
  return matchBreakpoint({
    sm: { cols: 1, side: false },   // <600dp  单栏
    md: { cols: 2, side: true },    // >=600dp 双栏（列表 + 详情并排）
    lg: { cols: 2, side: true },
    xl: { cols: 2, side: true },
  });
};

const ITEMS = [
  { id: "a", title: "编译管线", desc: "词法 → 语法 → 字节码 → 栈式 VM，109 个操作码。" },
  { id: "b", title: "软件光栅化 GUI", desc: "flex 布局 + JSX + 信号驱动，win32 / X11 / cocoa 三后端。" },
  { id: "c", title: "脚本打包器", desc: "把 .js 打成单个可执行文件，跨平台换 GOOS 即可。" },
  { id: "d", title: "宿主能力模块", desc: "fs / http / clipboard / 对话框 / 持久化，共用一套 NativeHost 契约。" },
];

// 列表：选中项高亮（selected() 是 signal，读它即订阅）
const List = () => (
  <column gap={6}>
    <view each={ITEMS} key="id">
      {(it, i) => (
        <row gap={6} padding={8} radius={6}
             background={selected() === i ? "#dce8ff" : "#f2f5fa"}
             onClick={() => setSelected(i)}>
          <text font={13} color="#334">{it.title}</text>
        </row>
      )}
    </view>
  </column>
);

// 详情：跟着 selected() 走
const Detail = () => (
  <column gap={6} padding={12} radius={8} background="#ffffff">
    <text font={14} color="#223">{ITEMS[selected()].title}</text>
    <text font={12} color="#667">{ITEMS[selected()].desc}</text>
  </column>
);

const thresholds = () => {
  const t = breakpoints();
  return ["sm", "md", "lg", "xl"].filter((k) => t[k] !== undefined)
    .map((k) => `${k}@${t[k]}`).join("  ");
};

let handle = null;
handle = render(
  <window title="自适应列表页" width={520} height={420}>
    <column gap={10} padding={14}>
      <text font={15}>{() => `breakpoint = ${bp()}   →   ${layout().side ? "双栏" : "单栏"}`}</text>
      <text font={11} color="#667">{() => "thresholds (dp):  " + thresholds()}</text>

      {/* 布局分支由断点名声明，不是散落在 N 处的 if */}
      <Switch>
        <Match when={() => layout().side}>
          <row gap={10}>
            <column width={210} gap={6}><List /></column>
            <column flex={1}><Detail /></column>
          </row>
        </Match>
        <Match when={() => true}>
          <column gap={8}>
            <List />
            <Detail />
          </column>
        </Match>
      </Switch>

      <row gap={8}>
        <button onClick={() => handle.resize(480, 420)}>480 · 单栏</button>
        <button onClick={() => handle.resize(700, 420)}>700 · 双栏</button>
        <button onClick={() => handle.resize(1000, 420)}>1000 · 双栏</button>
      </row>
    </column>
  </window>
);

// ===== 以下几行是给 Go 侧回归用的读入口（gfx/adaptive_list_test.go 直接跑
// 本文件，不另写一份脚本），demo 本身不需要它们 =====
globalThis.__bp = () => bp();
globalThis.__side = () => layout().side;
globalThis.__cols = () => layout().cols;
globalThis.__resize = (w, h) => handle.resize(w, h);
// 【坑 1】的可执行证据：这是**纯读数快照**（不订阅），首帧之后永远停在 "sm"。
// 回归会断言拖宽后它与 __bp() 不再相等 —— 静默失效就是这么发生的。
globalThis.__staleSnapshot = breakpoint();
