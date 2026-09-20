// gx/view 演示 (改写版): For / Show / Switch / Match
// 运行: go run . testdata/view_demo2.js
//
// 与原版 (testdata/view_demo.js) 的差别 —— 每一条都是为了少一次"我猜为什么它不更新":
//
//   1. 每行带 gen 徽标 (这一行是哪一次构建产生的), 头部还有全局 builds 计数:
//      "复用还是重建"从"得在行里打字再肉眼看字还在不在"变成一眼可见的数字。
//   2. 每个 prop 传什么都写在注释里: each / when / key 必须是取值函数, value /
//      disabled 也传函数才是响应式的 —— 写成快照不报错, 只会静默不更新。
//   3. Switch 的 fallback 可达 (原版三个按钮只能设三个已知相位, fallback 是死分支)。
//   4. 危险操作有状态: 空列表时 drop / shuffle / retitle / clear 自动禁用; 列表放进
//      <scroll>, 行数上限只是"别让演示失控"的保险 —— 没有滚动容器时, 多出来的行会
//      被窗口裁掉, 而裁掉的部分既画不出来也点不中。
//   5. keep-alive 有真凭据: 面板里挂了一个秒表 (setInterval + onCleanup), 隐藏期间
//      它继续走, 再显示时数字是跳过去的; onCleanup 只在分支真被销毁时才跑。
//   6. 交互日志条把"刚才发生了什么"写在界面上 —— 演示脚本自己解释自己。
//
// 三条纪律 (完整理由见 gfx/view.go 文件头):
//   · each / when 传取值函数 —— **直接传 signal 就是最简写法** (它本来就是函数):
//     each={rows} / when={open}。写成快照 (each={rows()} / when={open()}) 只有第一帧,
//     之后 signal 再变也不会重渲染 —— 这种写法内核现在会出声警告。
//   · key 传取值函数, 或字段名简写 key="id" (等价于 key={(r) => r.id}。字段名取不到时
//     退化为按位置匹配, 与不给 key 同义)。复用判定 = key + 引用同一性 + 下标;
//     行里不显示位置才加 stable, 代价是下标参数停在挂载时的值 (别拿 i 拼静态文本)。
//   · Show / Switch 是 keep-alive 显隐 (隐藏 = 摘出布局流, 子树保活), 不是 v-if。
//     要 v-if (每次显示都全新构建) 用函数子节点: {() => cond() ? <column/> : null}
import { h, render, createSignal, onCleanup, For, Show, Switch, Match } from "gox";

// ===== 状态 (界面全部派生自这几个 signal, 不存第二份) =====

const MAX_ROWS = 12;

let seq = 0; // 行 id 序号
let buildNo = 0; // 构建代次: 每次"渲染函数被调用"就 +1 (复用的行不会涨)

const mkRow = (title) => {
  seq = seq + 1;
  return { id: "r" + seq, title: title === "" ? "Row " + seq : title };
};

const [rows, setRows] = createSignal([mkRow("Alpha"), mkRow("Beta"), mkRow("Gamma")]);
const [draft, setDraft] = createSignal(""); // 新行标题 (受控输入)
const [open, setOpen] = createSignal(true); // Show 的条件
const [phase, setPhase] = createSignal("loading"); // Switch 的多状态
const [tags, setTags] = createSignal(["For", "Show", "Switch", "Match"]);
const [builds, setBuilds] = createSignal(0); // buildNo 的响应式影子: 普通变量不会触发重渲染
const [log, setLog] = createSignal(["点上面的按钮 —— 这里会解释刚才发生了什么"]);

const pushLog = (line) => setLog([line].concat(log()).slice(0, 3));
const phaseIs = (name) => phase() === name;
const canAdd = () => rows().length < MAX_ROWS;
const canShrink = () => rows().length > 0;

// 一次"构建" = 某行的渲染函数被调用一次 (复用的行不会走到这里)。返回值就是这一代的
// 代次号, 行把它烤进静态文本, 于是"这一行是哪一次构建的"写在脸上。
//
// 为什么敢在渲染函数里写 signal: 只有**读**才会建立依赖。这里只写不读, 所以驱动 For
// 的那个 effect 不会因为 setBuilds 再跑一轮 (要读它的那行 stats 是另一棵子树)。
const bump = () => {
  buildNo = buildNo + 1;
  setBuilds(buildNo);
  return buildNo;
};

// ===== 动作 (只在事件处理器里写 signal, 渲染函数里只读) =====

const addRow = () => {
  setRows(rows().concat([mkRow(draft())]));
  setDraft("");
  pushLog("add: 只构建新行 —— 其余行的 gen 不变");
};
const removeRow = (id) => {
  setRows(rows().filter((r) => r.id !== id));
  pushLog("remove " + id + ": 只有那一行被销毁 (它的 onCleanup 同时收尾)");
};
const dropLast = () => {
  const rs = rows();
  setRows(rs.slice(0, rs.length - 1));
  pushLog("drop last: 尾部删除不动其它行的下标 ⇒ 其余行一个都不重建");
};
const shuffle = () => {
  setRows(rows().slice().reverse());
  pushLog("shuffle: stable ⇒ 重排也不重建任何行, gen 与行内输入框全留着");
};
const retitleFirst = () => {
  setRows(rows().map((r, i) => (i === 0 ? { id: r.id, title: r.title + "*" } : r)));
  pushLog("retitle 1st: 换了对象引用 ⇒ 只重建第一行 (key 没变, 引用变了)");
};
const clearAll = () => {
  setRows([]);
  pushLog("clear: 行全销毁 ⇒ 露出 fallback (它保活, 只构建过一次)");
};
const swapFirstTwo = () => {
  const t = tags();
  setTags([t[1], t[0]].concat(t.slice(2)));
  pushLog("swap: 没有 stable ⇒ 移动过下标的行按新下标重渲染 (gen +2)");
};

// ===== 一行 = 一棵子树 (行宿主) =====
//
// gen 徽标是"这一行有没有被重建"的唯一硬证据: 行被重建 ⇒ 新的 gen + 行内 signal
// 回到初值; 行被复用 ⇒ 两个都原样。行内那个 note 是局部状态, 所以它天然是复用探针。
const Row = (p) => {
  const gen = bump();
  const [note, setNote] = createSignal("");
  return (
    <row gap={8} alignItems="center">
      <text font={11} color="#8a93a0" width={46}>{"gen " + gen}</text>
      <text font={13} width={96}>{p.row.title}</text>
      {/* model = value + onInput 一起给, 不用再手写那一对 (gfx/model.go) */}
      <input width={150} placeholder="row-local state" model={note} />
      <button padding={3} onClick={() => removeRow(p.row.id)}>remove</button>
    </row>
  );
};

// ===== 面板: 状态与定时器都长在分支里面 =====
//
// 隐藏不销毁 ⇒ 秒表继续走、输入的字还在; onCleanup 只在分支真被销毁时才跑 ——
// 这两件事都做成了界面上看得见的东西, 不用去读内核注释。
//
// ⚠️ 注意 `<Show>` 里写的是 {Panel} 而不是 <Panel/>。JSX 里大写标签是**当场调用**
// (parser 把 <Panel/> 降级成 Panel(null) 调用), 放在 children 位置就等于"脚本求值
// 时就跑了一遍组件体": createSignal/setInterval 立刻执行, onCleanup 因为"不在响应式
// 子节点里"退化成 no-op (内核会打一条警告), 定时器再也回收不掉, 也拿不到"第一次
// 显示才构建"的懒加载。传函数子节点 {Panel} 才是跟着分支走的组件。
const Panel = () => {
  const [note, setNote] = createSignal("");
  const [secs, setSecs] = createSignal(0);
  const timer = setInterval(() => setSecs((s) => s + 1), 1000);
  onCleanup(() => clearInterval(timer));
  return (
    <column gap={6}>
      <row gap={8} alignItems="center">
        <text font={12} width={188} color="#4a5560">panel-local input</text>
        <input width={188} placeholder="type, then hide / show" model={note} />
        <text font={12} color="#68707c">{() => (note() === "" ? "(empty)" : "kept: " + note())}</text>
      </row>
      <row gap={8} alignItems="center">
        <text font={12} width={188} color="#4a5560">panel-local ticker</text>
        <text font={12} color="#2e7d32">{() => "alive " + secs() + "s (kept running while hidden)"}</text>
      </row>
    </column>
  );
};

// ===== 界面 =====

render(
  <window title="gx/view — loops & conditions, mechanics visible" width={700} height={580}>
    <column gap={10} padding={14}>
      <text font={17}>gx/view — For / Show / Switch / Match</text>
      <text font={11} color="#8a93a0">
        {() => "rows " + rows().length + "/" + MAX_ROWS
          + "   ·   builds " + builds()
          + "   ·   phase " + phase()
          + "   ·   panel " + (open() ? "shown" : "hidden")}
      </text>

      {/* ---- 1 · 列表 (For) ---- */}
      <column gap={6} background="#f5f6f8" padding={10}>
        <row gap={6} alignItems="center">
          <text font={13} width={166}>1 · list — For (keyed + stable)</text>
          {/* model 管存值, onKeyDown 管回车提交 —— 两个都跑, 互不干扰 */}
          <input
            width={132}
            placeholder="new row title"
            model={draft}
            onKeyDown={(e) => { if (e.key === "Enter" && canAdd()) addRow(); }}
          />
          <button padding={4} disabled={() => !canAdd()} onClick={addRow}>add</button>
          <button padding={4} disabled={() => !canShrink()} onClick={dropLast}>drop last</button>
          <button padding={4} disabled={() => !canShrink()} onClick={shuffle}>shuffle</button>
          <button padding={4} disabled={() => !canShrink()} onClick={retitleFirst}>retitle 1st</button>
          <button padding={4} disabled={() => !canShrink()} onClick={clearAll}>clear</button>
        </row>
        <text font={11} color="#7a8391" wrap width={646}>
          gen = 这一行的构建代次, 复用的行 gen 不变。可在行内输入框打字: 重建会清空它。
          Enter 或 add 追加一行 (标题取自左边输入框, 留空自动命名)。
        </text>

        {/* 列表放进 <scroll>: 行数不设硬上限也能一直加 —— 没有滚动容器时, 多出来的
            行会被窗口裁掉, 而裁掉的部分既画不出来也点不中 */}
        <scroll height={142}>
          {/* each 传 signal 本身 (它本来就是取值函数); key="id" 是 key={(r) => r.id} 的简写;
              stable = 行不显示位置, 于是下标不参与复用判定 */}
          <For
            each={rows}
            key="id"
            stable
            fallback={
              <row gap={8} alignItems="center">
                <text font={12} color="#a0522d">list is empty — 这是 fallback, 它保活且只构建过一次</text>
              </row>
            }
          >
            {(row) => <Row row={row} />}
          </For>
        </scroll>

        <row gap={8} alignItems="center">
          <text font={11} color="#7a8391" width={166}>positional — For (no stable)</text>
          {/* 不带 stable: 下标参与复用判定, 所以移动过的行会重渲染 (gen 变), 没动的原样 */}
          <For each={tags} key={(t) => t}>
            {(t, i) => {
              const g = bump();
              return <text font={12} color="#3a4450">{"[gen " + g + "] " + (i + 1) + ". " + t}</text>;
            }}
          </For>
          <button padding={4} onClick={swapFirstTwo}>swap 1st / 2nd</button>
        </row>
      </column>

      {/* ---- 2 · 条件显示 (Show) ---- */}
      <row gap={8} alignItems="center">
        <text font={13} width={166}>2 · condition — Show (keep-alive)</text>
        <button padding={4} onClick={() => setOpen(!open())}>
          {() => (open() ? "hide panel" : "show panel")}
        </button>
        <text font={11} color="#7a8391">{() => (open() ? "在面板里打字, 然后隐藏" : "隐藏中: 状态没丢, 秒表还在走")}</text>
      </row>
      <Show
        when={open}
        fallback={
          <text font={12} color="#a0522d" wrap width={646}>
            panel hidden — 这是 fallback 分支。面板本身没被销毁: 再显示时你输入的字和秒表都还在。
          </text>
        }
      >
        {Panel}
      </Show>

      {/* ---- 3 · 多状态 (Switch / Match) ---- */}
      <row gap={8} alignItems="center">
        <text font={13} width={166}>3 · multi-state — Switch / Match</text>
        {/* disabled 也传函数才是响应式的; 传 phaseIs("loading") 只是当场的布尔快照 */}
        <button padding={4} disabled={() => phaseIs("loading")} onClick={() => setPhase("loading")}>loading</button>
        <button padding={4} disabled={() => phaseIs("ready")} onClick={() => setPhase("ready")}>ready</button>
        <button padding={4} disabled={() => phaseIs("error")} onClick={() => setPhase("error")}>error</button>
        <button padding={4} disabled={() => phaseIs("nope")} onClick={() => setPhase("nope")}>bad phase</button>
      </row>
      <Switch
        fallback={
          <row gap={8} alignItems="center">
            <text font={12} color="#a0522d">没有任何 Match 为真 ⇒ fallback (点 bad phase 就能走到这里)</text>
          </row>
        }
      >
        <Match when={() => phaseIs("loading")}>
          <row gap={8} alignItems="center">
            <progress value={0.4} width={190}></progress>
            <text font={12} color="#68707c">loading — 分支也是 keep-alive: 切走再切回来是同一批节点</text>
          </row>
        </Match>
        <Match when={() => phaseIs("ready")}>
          <row gap={8} alignItems="center">
            <text font={12} color="#2e7d32">ready — 数据到了</text>
            <button padding={4} onClick={() => setPhase("loading")}>reload</button>
          </row>
        </Match>
        <Match when={() => phaseIs("error")}>
          <row gap={8} alignItems="center">
            <text font={12} color="#b00020">failed — 重试还是放行?</text>
            <button padding={4} onClick={() => setPhase("loading")}>retry</button>
            <button padding={4} onClick={() => setPhase("ready")}>ignore</button>
          </row>
        </Match>
      </Switch>

      {/* ---- 交互日志: 把内核语义摊到界面上 ---- */}
      <column gap={2}>
        <text font={11} color="#8a93a0">what just happened</text>
        {/* 无 key 的列表: 前置插入会把内容顶下去, 按值比较 ⇒ 该重建的行才重建 */}
        <For each={log}>
          {(line) => <text font={11} color="#4a5560">{"· " + line}</text>}
        </For>
      </column>
    </column>
  </window>
);
