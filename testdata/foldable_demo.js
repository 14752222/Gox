// ============================================================
// 折叠屏 demo —— 一段代码，八种形态（S0 空壳工程）
// 运行: gox testdata/foldable_demo.js
//
// 形态矩阵（顶栏 8 个按钮，一键切换，不会崩）:
//   Single        单屏手机        480x800   flat       无折痕
//   DualFlat      展开但平展      1000x800  flat       有折痕(inactive) → tablet
//   DualHalf      半折双栏        1000x800  half-open  折痕 active     → dual
//   Tabletop      桌面/帐篷态     1000x800  half-open  横折痕 active   → 上下分栏
//   TriFold       三折屏          1400x800  half-open  两条 division  → dual
//   FoldReversed  反折            900x800   half-open  折痕右偏(反折)  → dual
//   Unknown       姿态未知        800x800   unknown    无折痕          → single
//   NoCapability  无折叠能力      800x800   flat       无折痕 + 能力关闭
//
// 另有两个非形态演示:
//   权限拒绝        —— gx/permission 报 denied 后 UI 降级（不崩、不空白）
//   能力 unsupported —— canIUse("foldable") 为 false 时走纯断点分支
//
// 演示的 API（全部来自 gx/screen + gx/viewport + gx/device + gx/permission）:
//   reportPosture()      —— 模拟宿主上报（demo 里由按钮驱动；真机上由
//                           GoxDisplayFold 宿主回调自动上报）
//   useLayoutMode()      —— posture + widthClass → suggested: single/dual/tablet
//   useBreakpoint()      —— 命名断点 sm/md/lg/xl
//   useReservedRegions() —— reservedRegions().division / occlusion 分组
//   useInsets()          —— 安全区（桌面为 0；真机上来自状态栏/挖孔）
//   canIUse("foldable")  —— 能力探测，unsupported 时降级
//
// ============================================================
// 【订阅纪律 · 隐藏考点】一定要读
// ============================================================
// hasFold() / posture() / regions() 是**纯读数，不订阅**；
// useXxx() 返回的取值函数（useLayoutMode()() 等）才订阅。
//
// 错误写法（会静默失效，无任何警告）:
//     const x = hasFold() ? useReservedRegions()() : null;
//   —— 首帧 hasFold() 为 false ⇒ 右侧的订阅调用根本没执行 ⇒ 这个 effect
//      零依赖 ⇒ 之后折起来**永不重跑**。
//
// 正确写法（本 demo 用的）:
//     先在函数体顶部**无条件**取一次订阅型读数，再决定显示什么。
//
// 注意：useLayoutMode / useBreakpoint / useReservedRegions 返回**取值函数**
// （两段式 useXxx()()），useInsets() 返回**对象**（一段式）。两者不要混。
// ============================================================

import { h, render } from "gx/gfx";
import { createSignal } from "gx/solid";
import {
  useLayoutMode, useBreakpoint, useReservedRegions, useInsets,
} from "gx/viewport";
import { reportPosture } from "gx/screen";
import { canIUse } from "gx/device";
import { reportPermission, permissionState } from "gx/permission";

const lm = useLayoutMode();      // () => {posture, widthClass, suggested, foldAware}
const bp = useBreakpoint();      // () => "sm"|"md"|"lg"|"xl"
const rr = useReservedRegions(); // () => {division:[...], occlusion:[...], all:[...]}
const insets = useInsets();      // {top,right,bottom,left}  (一段式: 直接是对象)

// ===== demo 状态（真机上这些都不存在）=====
// 开关一律走 createSignal: 函数 prop 里读 signal 才会自动重算。
// 普通变量在 solid 里不是响应式的 —— 改了它界面不会动, 也没有任何警告。
const [foldCapable, setFoldCapable] = createSignal(true);   // 能力 unsupported 演示
const [cameraDenied, setCameraDenied] = createSignal(false); // 权限拒绝演示

// 8 种形态的载荷表：每档 = 一次 ReportDisplayFold 的完整内容
const DEVICES = {
  Single: {
    label: "单屏",
    w: 480, h: 800, posture: "flat", hinge: null, regions: [],
  },
  DualFlat: {
    label: "展开平展",
    w: 1000, h: 800, posture: "flat",
    hinge: { x: 500, y: 0, width: 0, height: 800, orientation: "vertical" },
    regions: [{ kind: "division", x: 500, y: 0, width: 0, height: 800, active: false }],
  },
  DualHalf: {
    label: "半折双栏",
    w: 1000, h: 800, posture: "half-open",
    hinge: { x: 480, y: 0, width: 40, height: 800, orientation: "vertical" },
    regions: [
      { kind: "division", x: 480, y: 0, width: 40, height: 800, active: true },
      { kind: "occlusion", x: 20, y: 20, width: 60, height: 60, active: true },
    ],
  },
  Tabletop: {
    label: "桌面态",
    w: 1000, h: 800, posture: "half-open",
    hinge: { x: 0, y: 380, width: 1000, height: 40, orientation: "horizontal" },
    regions: [{ kind: "division", x: 0, y: 380, width: 1000, height: 40, active: true }],
  },
  TriFold: {
    label: "三折",
    w: 1400, h: 800, posture: "half-open",
    hinge: { x: 466, y: 0, width: 24, height: 800, orientation: "vertical" },
    regions: [
      { kind: "division", x: 466, y: 0, width: 24, height: 800, active: true },
      { kind: "division", x: 932, y: 0, width: 24, height: 800, active: true },
    ],
  },
  FoldReversed: {
    label: "反折",
    w: 900, h: 800, posture: "half-open",
    hinge: { x: 640, y: 0, width: 40, height: 800, orientation: "vertical" },
    regions: [{ kind: "division", x: 640, y: 0, width: 40, height: 800, active: true }],
  },
  Unknown: {
    label: "未知姿态",
    w: 800, h: 800, posture: "unknown", hinge: null, regions: [],
  },
  NoCapability: {
    label: "无折叠能力",
    w: 800, h: 800, posture: "flat", hinge: null, regions: [],
  },
};

// setDevice: 一键切换。每档都同时上报 posture + 尺寸 + 折痕/保留区，
// 然后**跟着 resize 窗口**（真机上由 Activity resize 驱动）。
function setDevice(name) {
  const d = DEVICES[name];
  if (!d) return;
  setFoldCapable(name !== "NoCapability");
  reportPosture({
    posture: d.posture,
    width: d.w, height: d.h,
    hinge: d.hinge || undefined,
    regions: d.regions,
  });
  if (globalThis.__win) globalThis.__win.resize(d.w, d.h);
}

// 权限演示：上报 camera=denied → UI 降级成"去设置"提示，而不是留白/报错
function toggleCamera() {
  setCameraDenied(!cameraDenied());
  reportPermission("camera", cameraDenied() ? "denied" : "granted");
}

// describePermissionState: 权限状态的显示词 (spec 见 docs/mobile-adaptation.md §12)。
// unknown 是"问不到"(本平台不提供), 不是"未授权" —— 两者对用户含义完全不同。
const describePermissionState = (s) => ({
  "granted": "已授权",
  "denied": "已拒绝（可再次申请）",
  "not-determined": "尚未申请",
  "restricted": "受系统策略限制",
  "limited": "部分授权",
}[s] || "未知（本平台不提供）");

// ===== 组件 =====

// 折痕避让: 只处理竖直 division（横向折痕走上下分栏，见 App）
const foldGutters = () => {
  rr(); // 【订阅纪律】先无条件订阅，再分支
  const divs = rr().division.filter((r) => r.active && r.width > 0 && r.height > 0);
  const vertical = divs.filter((r) => r.height >= r.width);
  if (!vertical.length) return { pad: 0, count: 0 };
  // 多条折痕（三折）时让出最宽的那条的一半 + 8px 呼吸
  const maxW = Math.max.apply(null, vertical.map((r) => r.width));
  return { pad: Math.round(maxW / 2) + 8, count: vertical.length };
};

const Badge = () => (
  <column gap={2}>
    <text font={12} color="#334">{() => "breakpoint: " + bp()}</text>
    <text font={12} color="#334">
      {() => "posture: " + lm().posture + "  →  suggested: " + lm().suggested}
    </text>
    <text font={11} color="#7a8">
      {() => {
        // 订阅 rr(): 切到 NoCapability(空 regions)时这行会重算。
        // canIUse("foldable") 在这里是纯读数演示 —— 桌面没有宿主声明, 恒 false;
        // 真机上由宿主 declareCapabilities("foldable") 驱动, 那时它才是权威。
        rr();
        const host = canIUse("foldable");
        const on = foldCapable();
        return "foldable 能力: " + (on ? "supported" : "unsupported") + (host ? "" : "（桌面无宿主声明）");
      }}
    </text>
  </column>
);

const Item = (props) => (
  <column background="#f4f6fb" padding={8} gap={2} radius={6}>
    <text font={13} color="#334">{props.title}</text>
    <text font={11} color="#889">{props.sub}</text>
  </column>
);

// 权限拒绝降级面板：camera 被拒时替代原内容，不留白、不报错。
// 注意读 signal 而不是 permissionState() —— 后者是纯读数不订阅, 写进函数 prop
// 首帧之后永不重算 (又一个"纯读数短路订阅"的现场演示)。真机上宿主上报
// denied 后会推 env 变化, permissionState() 需要包在 useXxx 式订阅里用。
const CameraPanel = () => (
  <column background={cameraDenied() ? "#fff2f0" : "#eef1fb"} padding={12} gap={6} radius={6}>
    <text font={13} color={cameraDenied() ? "#a33" : "#223"}>
      {() => "相机权限: " + describePermissionState(permissionState("camera"))}
    </text>
    <text font={11} color="#667">
      {() => (cameraDenied()
        ? "降级路径：这里换成「去设置开启」入口，界面不空白、不报错。 (宿主缓存: " + permissionState("camera") + ")"
        : "点上方「权限拒绝」按钮模拟宿主上报 denied。")}
    </text>
  </column>
);

const App = () => {
  const btn = (name) => (
    <button onClick={() => setDevice(name)}>{DEVICES[name].label}</button>
  );

  // 主体内容必须是函数 prop: suggested/dual/horizontal 都依赖姿态与保留区,
  // 每次重算时在**函数内部**重新取 —— 放在组件体里就是首帧快照, 永不更新
  // (组件体只跑一次; 函数 prop 才会被 solid 包成 effect)。
  const body = () => {
    lm(); rr(); // 【订阅纪律】无条件订阅, 再分支 —— 三元短路会吃掉订阅
    const suggested = foldCapable() ? lm().suggested : "single";
    const dual = suggested === "dual" || suggested === "tablet";
    const horizontal = rr().division.some((r) => r.active && r.width > r.height);
    const g = foldGutters();
    if (horizontal) {
      return (
        <column gap={0} flex={1}>
          <column gap={6} flex={1} paddingBottom={g.pad}>
            <Item title="上栏" sub="横向折痕上方（桌面态）" />
          </column>
          <column gap={6} flex={1} paddingTop={g.pad}>
            <Item title="下栏" sub="横向折痕下方（桌面态）" />
          </column>
        </column>
      );
    }
    if (dual) {
      return (
        <row gap={0} flex={1}>
          <column gap={6} flex={1} paddingLeft={g.pad} paddingRight={g.pad}>
            <Item title="1. 断点单双栏" sub="useBreakpoint" />
            <Item title="2. 折痕避让" sub="useReservedRegions().division" />
            <Item title="3. 姿态建议"
              sub={() => { lm(); return "suggested = " + (foldCapable() ? lm().suggested : "single"); }} />
          </column>
          <column gap={6} flex={1}>
            <CameraPanel />
            <Item title="右栏" sub="宽屏/半折时出现，同一棵组件树" />
          </column>
        </row>
      );
    }
    return (
      <column gap={6} flex={1}>
        <Item title="单栏" sub={"suggested = " + suggested + "（sm 或能力 unsupported）"} />
        <CameraPanel />
      </column>
    );
  };

  return (
    <column gap={10} flex={1}
      padding={[insets.top + 10, insets.right + 12, insets.bottom + 10, insets.left + 12]}>
      {/* 顶栏: 8 个形态按钮 */}
      <row gap={6} wrap={true}>
        {btn("Single")}{btn("DualFlat")}{btn("DualHalf")}{btn("Tabletop")}
        {btn("TriFold")}{btn("FoldReversed")}{btn("Unknown")}{btn("NoCapability")}
      </row>
      {/* 降级演示开关 + 读数徽章 */}
      <row gap={6}>
        <button onClick={toggleCamera}>权限拒绝</button>
        <button onClick={() => setFoldCapable(!foldCapable())}>
          {() => (foldCapable() ? "关闭折叠能力" : "开启折叠能力")}
        </button>
        <row flex={1} />
        <Badge />
      </row>

      {/* 保留区读数指示条 */}
      <text font={11} color="#a0a">
        {() => {
          // 【订阅纪律】这里必须用订阅型 rr() 而不是纯读数 regions() ——
          // 纯读数不订阅, 首帧空了之后永不重算 (本考点的现场演示)。
          const act = rr().all.filter((x) => x.active);
          if (!act.length) return "无活动保留区（非折叠屏 / 已展平 / 能力 unsupported）";
          return act.map((x) =>
            `${x.kind} #${x.id} @(${x.x},${x.y}) ${x.width}x${x.height}`).join("   ");
        }}
      </text>

      {body}

      <text font={10} color="#aab">
        {"同一段代码：真机上由宿主 (GoxDisplayFold) 自动上报 posture/regions，桌面 demo 用按钮模拟。"}
      </text>
    </column>
  );
};

// render 的根元素必须是 <window>（组件根不受支持，静默不建窗）
globalThis.__win = render(
  <window title="foldable demo" width={1020} height={820}>
    <App />
  </window>
);

// 初始上报放进 setTimeout: 顶层同步上报会撞上「render 完成后事件泵还没跑、
// 首轮 effect 重算被吞」的时序（与真机一致 —— 宿主上报本来就是异步回调）。
setTimeout(() => {
  reportPermission("camera", "granted");
  setDevice("DualHalf");
}, 300);

// 心跳: 规避「最后一个 timer 回调里触发 reportPosture 后，事件泵进入
// pump(0) 无限期等待、脏区来不及上屏」的泵时序问题（引擎待查，见
// vm/timers.go runTimersLoop 的 !found 分支）。空回调 + 250ms 间隔，
// CPU 开销可忽略；真机上事件由宿主持续驱动，不存在此问题。
setInterval(() => {}, 250);
