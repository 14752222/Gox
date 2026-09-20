// gx/view 演示: 元素级指令 each / show + Switch / Match。
// 运行: go run . testdata/view_demo.js
//
// 2026-09-20: 列表与条件从 `<For>` / `<Show>` 组件改成了**元素级指令**
// (each / show, 写在元素上), 语义一字未改 —— 本演示就是同一份逻辑的指令写法:
//   <For each={x}>…</For>   →   <view each={x}>…</view>
//   <Show when={x}>…</Show> →   <view show={x}>…</view>
// 这里用 <view> (布局透明容器) 是为了与旧的 <For> / <Show> 逐像素一致;
// 换成 <row each={rows}> 就是"每一项一个盒子"。
//
// 建议手动验证 (每一条都在演示复用判定, 光看截图看不出区别):
//   1. "list (keyed + stable)": 每行有自己的输入框 (行内 signal) —— 它是
//      复用是否真的发生的照妖镜: 某行一旦被重建, 输入的字就回到初值。
//      点 shuffle 重排 / drop last 删尾行: 文字**跟着行走**, 一行都不重建。
//   2. 点 add: 只渲染新行; clear: 列表为空 → 显示 fallback (懒构建, 只构建一次)。
//   3. "condition (show)" 面板: 隐藏只是摘出布局流, 子树保活 —— 面板里输入的文字
//      在隐藏后仍在, 再显示瞬间切回 (这就是 show 不做 v-if 的原因, 见
//      gfx/view.go 文件头: 本引擎销毁过的静态子树无法复活)。
//   4. "branch (Switch / Match)": 按声明序取第一个为真的 Match, 都不真用 fallback。
//   5. "tags (unkeyed)": 静态数组 + 下标写进静态文本 —— 下标参与复用判定,
//      于是重排后序号一定是对的 (代价见文档)。
//
// 界面文案用英文, 与 kit_demo / resource_demo / dev_panel_demo 保持一致。
import { h, render, createSignal, Switch, Match } from "gox";

// ===== 状态 =====

let seq = 0;
const [rows, setRows] = createSignal([
  { id: "a", title: "Alpha" },
  { id: "b", title: "Beta" },
  { id: "c", title: "Gamma" },
]);
const [open, setOpen] = createSignal(true);
const [draft, setDraft] = createSignal("");
const [phase, setPhase] = createSignal("loading");

// ===== 行组件: 行内自带状态 (保住它就证明这一行没有被重建) =====

const RowCard = (p) => {
  const [note, setNote] = createSignal("");
  return (
    <row gap={8} note={p.row.id}>
      <text font={13} width={72}>{p.row.title}</text>
      <input width={150} value={note} onInput={(e) => setNote(e.value)} />
    </row>
  );
};

const addRow = () => {
  seq = seq + 1;
  setRows(rows().concat([{ id: "n" + seq, title: "Row " + seq }]));
};
const dropLast = () => setRows(rows().slice(0, rows().length - 1));
const shuffle = () => setRows(rows().slice().reverse());
const clearAll = () => setRows([]);

// ===== 界面 =====

render(
  <window title="gx/view — declarative view" width={580} height={470}>
    <column gap={10} padding={12}>
      <text font={16}>gx/view — declarative loops &amp; conditions</text>

      <column gap={6} background="#f5f6f8" padding={10}>
        <row gap={8}>
          <text font={13} width={176}>list (keyed + stable)</text>
          <button padding={4} onClick={addRow}>add</button>
          <button padding={4} onClick={dropLast}>drop last</button>
          <button padding={4} onClick={shuffle}>shuffle</button>
          <button padding={4} onClick={clearAll}>clear</button>
        </row>
        <view
          each={() => rows()}
          key={(r) => r.id}
          stable
          fallback={<row note="list-empty"><text font={12} color="#a0522d">empty list: fallback here</text></row>}
        >
          {(row) => <RowCard row={row} />}
        </view>
      </column>

      <row gap={8}>
        <text font={13} width={176}>condition (show, keep-alive)</text>
        <button padding={4} onClick={() => setOpen(!open())}>{() => open() ? "hide" : "show"}</button>
      </row>
      <view show={open}
        fallback={<row note="panel-off"><text font={12} color="#a0522d">panel hidden (fallback)</text></row>}
      >
        <row gap={8} note="panel-on">
          <text font={12} width={132}>type here:</text>
          <input width={150} model={draft} />
          <text font={12} color="#68707c">{() => "draft=" + draft()}</text>
        </row>
      </view>

      <row gap={8}>
        <text font={13} width={176}>branch (Switch / Match)</text>
        <button padding={4} onClick={() => setPhase("loading")}>loading</button>
        <button padding={4} onClick={() => setPhase("ready")}>ready</button>
        <button padding={4} onClick={() => setPhase("error")}>error</button>
      </row>
      <Switch fallback={<row note="phase-unknown"><text font={12} color="#a0522d">unknown phase (fallback)</text></row>}>
        <Match when={() => phase() === "loading"}>
          <row note="phase-loading"><progress value={0.5} width={220}></progress></row>
        </Match>
        <Match when={() => phase() === "ready"}>
          <row note="phase-ready"><text font={12} color="#2e7d32">ready</text></row>
        </Match>
        <Match when={() => phase() === "error"}>
          <row note="phase-error"><text font={12} color="#b00020">failed</text></row>
        </Match>
      </Switch>

      <column gap={4}>
        <text font={13}>tags (unkeyed: index baked into static text; host follows parent direction)</text>
        <row gap={10}>
          <view each={["go", "jsx", "declarative"]}>
            {(tag, i) => <text font={12}>{i + 1 + "." + tag}</text>}
          </view>
        </row>
      </column>

      <text font={12} color="#68707c">
        each / show hosts are transparent (view = Fragment): vertical in column, horizontal in row.
      </text>
    </column>
  </window>
);
