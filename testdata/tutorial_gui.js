// ============================================================================
// §1 的最小 GUI 骨架: 一个窗口 + 信号 + 指令
//
// 运行:  gox testdata/tutorial_gui.js
//
// 现象: 点 "+1" 计数增长; 输入框里打的字实时镜像; toggle 显隐详情块
//       (keep-alive: 藏起来不销毁, 再显示时状态原样)。
//
// 四条纪律 (每条都是"不这么写就静默失效"的类型):
//   ① 用了 JSX 就必须 import h —— JSX 在 parser 层降级成 h(...) 调用;
//   ② 响应式的东西一律**传函数**: value={() => x()} / show={x} / each={rows},
//      写成快照 value={x()} 只有第一帧是对的, 之后不报错也不更新;
//   ③ each / show 的 children 位置用 {函数/组件} 而不是 <大写标签/>;
//   ④ 条件渲染别指望"销毁再挂回来" —— 静态子树销毁后响应式接线就断了, 用 show 保活。
// ============================================================================
import { h, render, createSignal } from "gox";

// 状态: 建在模块作用域 = 跨函数共享 (没有额外的 store 框架)
const [count, setCount] = createSignal(0);
const [draft, setDraft] = createSignal("");
const [showDetail, setShowDetail] = createSignal(false);
const [rows, setRows] = createSignal([
  { id: "a", title: "第一行" },
  { id: "b", title: "第二行" },
]);

function App() {
  return (
    <column gap={10} padding={14}>
      {/* 响应式文本: 子节点传函数, 读到的信号变化时自动更新 */}
      <text font={20}>{() => "count: " + count()}</text>

      <row gap={8}>
        <button padding={6} onClick={() => setCount((c) => c + 1)}>+1</button>
        <button padding={6} onClick={() => setCount(0)}>reset</button>
        <button padding={6} onClick={() => setShowDetail((v) => !v)}>toggle</button>
      </row>

      {/* model 指令: 一条接好读(value)与写(onInput), 等价于 Vue 的 v-model */}
      <input width={220} placeholder="type here" model={draft} />
      <text font={13}>{() => "draft = " + (draft() === "" ? "(空)" : draft())}</text>

      <separator />

      {/* each 指令: keyed 复用列表 (行内状态在重排/增删时保留) */}
      <view each={rows} key="id">
        {(row) => <text font={13}>{() => "· " + row.title}</text>}
      </view>

      {/* show 指令: keep-alive 显隐 —— 首次显示才构建, 之后只摘出布局流不销毁 */}
      <view show={showDetail}>
        <rect width={220} height={36} radius={8} background="#e8eef7">
          <text font={12}>detail panel (keep-alive)</text>
        </rect>
      </view>
    </column>
  );
}

// window 标签只是"窗口配置的载体": render 会把 title/width/height 读出来,
// 布局根换成它唯一的子元素 (它自己不进树)。render 返回窗口句柄, 可调多次开多窗口。
render(
  <window title="Tutorial GUI" width={520} height={420}>
    <App />
  </window>
);
