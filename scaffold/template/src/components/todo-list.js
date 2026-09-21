// 列表 + 输入绑定 —— 这份模板里最值得抄的两个东西都在这里。
//
// 1) `each` 指令（写在元素上，对标 v-for）
//    <view each={todos} key="id">{(t) => <Row todo={t} />}</view>
//    · each 收的是**取值函数**：直接传 signal 就是最简写法（signal 本来就是函数）。
//      写成 each={todos()} 只是第一帧的快照，之后列表再怎么变都不会重渲染。
//    · key="id" 是 key={(r) => r.id} 的简写。复用判定 = key + 引用同一性 + 下标：
//      命中的行原样复用（行内的输入框、局部 signal 全留着），只有真变了的行才重建。
//      ⚠️ 所以 toggle 那里是"造一个新对象"而不是原地改字段 —— 引用没变的话行不会重建。
//    · fallback 是列表为空时显示的保活分支。
//
// 2) `model` 指令（受控组件的双向绑定，等价 v-model）
//    <input model={draft} /> 一条指令接好读（value）与写（onInput）。
//    手写版是 value={() => draft()} + onInput={(e) => setDraft(e.value)} —— 漏掉写方向就"打不进字"。
//    model 收 signal（createSignal 的 getter 自带 .set）或 [get, set] 二元组。
import { h } from "gox";
import { colors, space, font } from "../theme.js";
import { todos, setTodos, draft, setDraft, note, setNote } from "../store.js";

// 行 id 序号。初始那几条用的是 t1..t3，这里从 100 起，避免撞号。
let seq = 100;
const mkId = () => {
  seq = seq + 1;
  return "t" + seq;
};

// 一行 = 一棵子树。组件从 props 里拿数据和回调（不用 import 全局动作，方便复用）。
const Row = (p) => (
  <row gap={space.sm} alignItems="center">
    {/* checkbox 是完全受控的：显示只看 checked，点击只派发 onClick，值落地要自己写回列表。
        这里"值"派生自列表项，所以不走 model（model 需要一个可写的 signal）。 */}
    <checkbox checked={p.todo.done} onClick={() => p.onToggle(p.todo.id)} />
    <text
      font={font.md}
      width={300}
      color={() => (p.todo.done ? colors.muted : colors.text)}
    >
      {p.todo.title}
    </text>
    <button padding={3} onClick={() => p.onRemove(p.todo.id)}>删除</button>
  </row>
);

export function TodoList() {
  // 派生值：不另存一份状态，直接算
  const open = () => todos().filter((t) => !t.done).length;

  const add = () => {
    const title = draft().trim();
    if (title === "") {
      setNote("输入为空，没有添加");
      return;
    }
    setTodos(todos().concat([{ id: mkId(), title: title, done: false }]));
    setDraft("");
    setNote("已添加：" + title);
  };

  const toggle = (id) => {
    // 造**新对象**：引用变了，那一行才会重建（其余行按 key 复用，一个都不动）
    setTodos(todos().map((t) => (t.id === id ? { id: t.id, title: t.title, done: !t.done } : t)));
  };

  const remove = (id) => {
    setTodos(todos().filter((t) => t.id !== id));
    setNote("已删除 " + id);
  };

  const clearDone = () => {
    setTodos(todos().filter((t) => !t.done));
    setNote("已清掉完成项");
  };

  return (
    <column gap={space.sm}>
      <row gap={space.sm} alignItems="center">
        {/* model 管存值，onKeyDown 管回车提交 —— 两个都跑，互不干扰 */}
        <input
          width={260}
          placeholder="要做什么？回车或点 add"
          model={draft}
          onKeyDown={(e) => {
            if (e.key === "Enter") add();
          }}
        />
        <button padding={4} onClick={add}>add</button>
      </row>

      <text font={font.sm} color={colors.muted}>
        {() => open() + " 项未完成 / 共 " + todos().length + " 项"}
      </text>

      {/* 列表放进 <scroll>：溢出的内容既画不出来也点不中（绘制与命中用同一个视口） */}
      <scroll height={170}>
        <view
          each={todos}
          key="id"
          fallback={<text font={font.md} color={colors.warn}>列表空了 —— 这是 fallback 分支</text>}
        >
          {(t) => <Row todo={t} onToggle={toggle} onRemove={remove} />}
        </view>
      </scroll>

      <row gap={space.sm} alignItems="center">
        <button padding={4} disabled={() => todos().length === 0} onClick={clearDone}>清掉已完成</button>
        {/* note 是 signal，直接放在 children 位置即为响应式文本 */}
        <text font={font.sm} color={colors.muted}>{note}</text>
      </row>
    </column>
  );
}
