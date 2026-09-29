// 示例应用 1: TODO 任务清单 (T15 交付之一)。
// 运行: go run . testdata/apps/todo.js
//
// 展示的能力 (刻意每个能力只代表性地用一次, 其余保持朴素):
//   1. gx/solid 细粒度响应式: createSignal + 函数子节点;
//   2. gx/view 的 each 指令: keyed 复用 —— 编辑某行的字, 点"添加"后
//      文字跟着行走 (没有被重建);
//   3. 表单组件: input (value + onInput 受控) / checkbox / button;
//   4. gx/storage 持久化: 每次变更立即落盘, 重启应用列表原样回来
//      (存储文件在 <appDataDir>/storage.json, 可用 GOX_STORAGE_DIR 重定向);
//   5. 条件渲染: 过滤切换用信号, 空态用 each 的 fallback。
//
// 与 view_demo.js 的分工: 那里教"复用判定怎么看", 这里是"用" ——
// 一个真实的小应用该有的结构: 状态 → 派生 → 持久化 → 界面。
import { h, render, createSignal } from "gox";
import { setStorage, getStorage } from "gx/storage";

// ===== 状态 =====

let seq = 0;
const newTodo = (title) => {
  seq = seq + 1;
  return { id: "t" + seq, title, done: false };
};

// 重启恢复: 存储里有就用存的 (id 续着最大编号走, 避免重启后撞车)
const saved = getStorage("gox-todo.items");
let restored = Array.isArray(saved) ? saved : [];
if (restored.length === 0) {
  restored = [newTodo("跑通 gox dev"), newTodo("给 TODO 加个过滤")];
}
for (let i = 0; i < restored.length; i++) {
  const n = Number(String(restored[i].id).slice(1));
  if (n > seq) seq = n;
}

const [todos, setTodos] = createSignal(restored);
const [draft, setDraft] = createSignal("");
const [filter, setFilter] = createSignal("all"); // all | active | done

// ===== 派生 =====

const shown = () => {
  const all = todos();
  if (filter() === "active") return all.filter((t) => !t.done);
  if (filter() === "done") return all.filter((t) => t.done);
  return all;
};
const leftCount = () => todos().filter((t) => !t.done).length;

// 变更统一走这里: 改状态 + 落盘, 界面自动跟着动
const mutate = (fn) => {
  setTodos(fn(todos()));
  setStorage("gox-todo.items", todos());
};

// ===== 动作 =====

const add = () => {
  const title = draft().trim();
  if (!title) return;
  mutate((all) => all.concat([newTodo(title)]));
  setDraft("");
};
const toggle = (id) =>
  mutate((all) =>
    all.map((t) => (t.id === id ? { id: t.id, title: t.title, done: !t.done } : t))
  );
const remove = (id) => mutate((all) => all.filter((t) => t.id !== id));
const clearDone = () => mutate((all) => all.filter((t) => !t.done));

// ===== 界面 =====

const TodoRow = (p) => (
  <row gap={8} note={p.todo.id}>
    <checkbox checked={() => p.todo.done} onClick={() => toggle(p.todo.id)} />
    <text
      font={13}
      width={200}
      color={() => (p.todo.done ? "#98a0aa" : "#1c2430")}
    >{() => p.todo.title}</text>
    <button padding={2} onClick={() => remove(p.todo.id)}>删</button>
  </row>
);

const FilterBtn = (p) => (
  <button
    padding={4}
    background={() => (filter() === p.name ? "#3355aa" : "#e8ecf4")}
    color={() => (filter() === p.name ? "#ffffff" : "#1c2430")}
    onClick={() => setFilter(p.name)}
  >{p.label}</button>
);

render(
  <window title="TODO — Gox 示例应用" width={380} height={460}>
    <column gap={10} padding={14}>
      <text font={17}>TODO</text>

      <row gap={6}>
        <input width={210} placeholder="要做什么?" value={draft}
          onInput={(e) => setDraft(e.value)} />
        <button padding={6} onClick={add}>添加</button>
      </row>

      <row gap={6}>
        <FilterBtn name="all" label="全部" />
        <FilterBtn name="active" label="未完成" />
        <FilterBtn name="done" label="已完成" />
        <button padding={4} onClick={clearDone}>清已完成</button>
      </row>

      <view each={shown} key="id" fallback={<text color="#a0522d">这一类没有任务</text>}>
        {(row) => <TodoRow todo={row} />}
      </view>

      <text color="#68707c" font={12}>
        {() => `剩 ${leftCount()} 项未完成 / 共 ${todos().length} 项 (已自动保存)`}
      </text>
    </column>
  </window>
);
