// 根组件：折叠屏四场景。
//
// 每个场景回答一个具体问题，四块并排放：
//
//   S1 单双栏自适应   布局只信宽度（widthClass / useWindowInfo），不信姿态
//   S2 折痕避让       avoidReserved="division" 让内容主动让开折痕那条带
//   S3 Tabletop       layoutMode().suggested === "tablet" → 上下分屏
//   S4 跨屏拖拽       窗口在两个物理屏之间移动时读到的 display 变化
//
// 全部读数在场景里都**先无条件取一次**（两段式 useXxx() 先拿 getter 再调），
// 再进分支 —— 把订阅型读数写进三元/短路的某条分支是静默坑：
// 首帧没走到的分支不建立订阅，effect 零依赖、永不重跑，界面就像"死"了一样。
import { h } from "gox";
import { createSignal } from "gox";
import {
  useBreakpoint,
  widthClass,
  layoutMode,
  hasFold,
  reservedRegions,
} from "gx/viewport";
import { posture, hinge, usePosture, screenOf, useWindowInfo } from "gx/screen";
import { colors, space, font } from "./theme.js";
import { FORMS, applyForm, form, log, snap, refresh, logText } from "./store.js";
import { postureText, hingeText, suggestedText, panes } from "./lib/fold.js";

// ===== 通用小件 =====

function Card(p) {
  return (
    <column gap={space.xs} padding={space.sm} background={colors.panel}
            border={colors.border} radius={6}>
      <text font={font.md} color={colors.accent}>{p.title}</text>
      {p.children}
    </column>
  );
}

function KV(p) {
  return (
    <row gap={space.xs} alignItems="center">
      <text font={font.xs} color={colors.muted} width={132}>{p.k}</text>
      <text font={font.xs} color={colors.text}>{p.v}</text>
    </row>
  );
}

// ===== S1：单双栏自适应（只信宽度）=====

function ScenarioAdaptive() {
  // 两段式：外层绑窗口返回 getter，内层再调才订阅 + 取值。
  const getWin = useWindowInfo();
  const bp = useBreakpoint();

  // 无条件取一次订阅型读数，再进分支 —— 别塞进三元里。
  const w = () => getWin().width;
  const cls = () => widthClass();

  const Cols = () => {
    bp();
    const n = panes(w());
    const items = [];
    for (let i = 0; i < 6; i++) items.push(<text font={font.xs} color={colors.muted}>· 条目 {i + 1}</text>);
    // 单栏：一列直排；双栏：把前 3 条与后 3 条分到两列。
    if (n === 1) return <column gap={2}>{items}</column>;
    return (
      <row gap={space.md} alignItems="top">
        <column gap={2}>{items.slice(0, 3)}</column>
        <column gap={2}>{items.slice(3)}</column>
      </row>
    );
  };

  return (
    <Card title="S1 · 单双栏自适应（只信宽度）">
      <KV k="窗口宽" v={() => w() + " px → " + panes(w()) + " 栏"} />
      <KV k="widthClass()" v={cls} />
      <KV k="断点" v={() => bp()} />
      <Cols />
      <text font={font.xs} color={colors.muted} wrap width={300}>
        上下折展开后宽度可能仍 &lt; 600dp，所以列数只由宽度决定 —— 用 posture() 推布局
        必然翻车（官方时序里尺寸变化还早于折叠状态变化，会落后一帧）。
      </text>
    </Card>
  );
}

// ===== S2：折痕避让 =====

function ScenarioAvoid() {
  // 注意：这个函数体里**不能**出现名叫 h 的局部变量 —— JSX 会降级成 h(...)
  // 调用，把 h 覆盖掉就是"整块界面变成 null is not a function"。
  const rr = reservedRegions();
  const hg = hinge();

  // 避让是 opt-in：内核元素默认不避让（老界面不会莫名位移）。
  // 折痕带只是"分隔"，不一定有内容遮挡 —— 是否让开由应用决定。
  return (
    <Card title="S2 · 折痕避让（avoidReserved）">
      <KV k="折痕" v={() => hingeText(hg)} />
      <KV k="保留区" v={() => "division " + rr.division.length + " 段"} />
      <column gap={2} padding={space.xs} background={colors.bg} radius={4}>
        <text font={font.xs} color={colors.muted}>未避让（内容横跨折痕）：</text>
        <row gap={2} height={18} background={"linear-gradient(to right, #cfe3ff, #cfe3ff 48%, " + colors.hinge + " 48%, " + colors.hinge + " 52%, #cfe3ff 52%)"} />
      </column>
      <column gap={2} padding={space.xs} background={colors.bg} radius={4}>
        <text font={font.xs} color={colors.muted}>已避让（avoidReserved="division"）：</text>
        <row gap={0} height={18}>
          <row flexGrow={1} background={colors.left} />
          <column width={14} background={colors.hinge} border={colors.hingeBorder} />
          <row flexGrow={1} background={colors.right} />
        </row>
      </column>
      <text font={font.xs} color={colors.muted} wrap width={300}>
        avoidReserved 写在需要避让的容器上；<text color={colors.text}>scroll 上不适用</text>
        （会收缩滚动可用区、与滚动条语义打架，内核忽略并告警）。
      </text>
    </Card>
  );
}

// ===== S3：Tabletop =====

function ScenarioTabletop() {
  const m = layoutMode();
  const p = usePosture();

  const Tabletop = () => {
    p();
    const sug = layoutMode().suggested;
    if (sug !== "tablet") {
      return <text font={font.xs} color={colors.muted}>当前不是 Tabletop 形态（suggested = {sug}）</text>;
    }
    // 横折：上下两段。上段当"显示屏"，下段当"操作台"。
    return (
      <column gap={2}>
        <row height={46} background={colors.left} radius={4} alignItems="center"
             justifyContent="center">
          <text font={font.xs} color={colors.muted}>上段：内容 / 画面</text>
        </row>
        <row height={14} background={colors.hinge} border={colors.hingeBorder} />
        <row height={46} background={colors.right} radius={4} alignItems="center"
             justifyContent="center">
          <text font={font.xs} color={colors.muted}>下段：操作台</text>
        </row>
      </column>
    );
  };

  return (
    <Card title="S3 · Tabletop（横折上下分屏）">
      <KV k="layoutMode()" v={() => JSON.stringify(layoutMode())} />
      <KV k="suggested" v={() => layoutMode().suggested + " — " + suggestedText(layoutMode().suggested)} />
      <KV k="hingeOrientation" v={() => hinge() ? hinge().orientation : "—"} />
      <Tabletop />
      <text font={font.xs} color={colors.muted} wrap width={300}>
        suggested 只是建议 —— 框架永不动你的布局。上下折用 hingeOrientation() === "horizontal"
        区分，别拿宽度猜。
      </text>
    </Card>
  );
}

// ===== S4：跨屏拖拽 =====

function ScenarioCrossDisplay() {
  const getWin = useWindowInfo();

  const DisplayInfo = () => {
    const s = screenOf();
    if (!s) return <text font={font.xs} color={colors.muted}>（还没有窗口所在屏）</text>;
    return <text font={font.xs} color={colors.text}>
      {"id=" + s.id + "  " + s.width + "×" + s.height + "  scale=" + s.scale + (s.foldable ? "  foldable" : "")}
    </text>;
  };

  return (
    <Card title="S4 · 跨屏拖拽（窗口换屏）">
      <KV k="窗口尺寸" v={() => getWin().width + "×" + getWin().height} />
      <KV k="所在屏" v={() => getWin().screenId} />
      <DisplayInfo />
      <text font={font.xs} color={colors.muted} wrap width={300}>
        把窗口拖到另一块屏（或调 moveTo）时，screenOf() / windowInfo().screenId 会变。
        折痕坐标是<text color={colors.text}>显示器坐标</text>（设备像素）—— 全屏时等于窗口坐标，
        分屏/自由窗口下要减去窗口在屏上的原点。
      </text>
    </Card>
  );
}

// ===== 顶栏与读数面板 =====

function FormBar() {
  const buttons = FORMS.map((f) =>
    <button padding={4} onClick={() => applyForm(f.key)}
            background={() => (form() === f.key ? colors.accent : colors.panel)}
            color={() => (form() === f.key ? colors.accentFg : colors.text)}>
      {f.name}
    </button>
  );
  return (
    <column gap={space.xs}>
      <text font={font.md} color={colors.text}>形态切换（点一下 = 往内核上报一次姿态）</text>
      <row gap={space.xs} wrap>{buttons}</row>
    </column>
  );
}

function Readout() {
  const getP = usePosture();
  return (
    <Card title="读数面板（纯读数，随上报刷新）">
      <row gap={space.sm} alignItems="center">
        <text font={font.xs} color={colors.muted}>posture：</text>
        <text font={font.md} color={colors.accent}>{() => postureText(getP())}</text>
        <spacer flexGrow={1} />
        <button padding={4} onClick={refresh}>重读</button>
      </row>
      <column gap={1} padding={space.xs} background={colors.bg} radius={4}>
        <text font={font.xs} color={colors.text} wrap width={620}>{snap}</text>
      </column>
      <text font={font.xs} color={colors.muted}>事件：{() => logText(log())}</text>
    </Card>
  );
}

export function App() {
  // 结构性信号：有折痕的机器恒 true，不随折叠/平展 flip-flop（否则列数会跟着姿态跳）。
  const folded = hasFold();

  return (
    <column gap={space.sm} padding={space.md} background={colors.bg}>
      <row gap={space.sm} alignItems="center">
        <text font={font.xl} color={colors.text}>折叠屏适配 · 四场景</text>
        <spacer flexGrow={1} />
        <tag color={colors.accent}>{() => (hasFold() ? "带折痕设备" : "无折痕设备")}</tag>
      </row>
      <FormBar />
      <Readout />
      <row gap={space.sm} wrap alignItems="top">
        <ScenarioAdaptive />
        <ScenarioAvoid />
      </row>
      <row gap={space.sm} wrap alignItems="top">
        <ScenarioTabletop />
        <ScenarioCrossDisplay />
      </row>
      <text font={font.xs} color={colors.muted} wrap width={640}>
        四条纪律：① 布局只信宽度，不信姿态；② 订阅型读数先取再分支，别塞进三元；
        ③ 折痕/保留区坐标是显示器坐标；④ 没人上报就恒为平展 —— 真机由平台壳工程上报。
      </text>
    </column>
  );
}
