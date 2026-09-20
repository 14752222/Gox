# gx/model：受控组件的双向绑定（接口设计）

> 状态：**已落地**（`gfx/model.go` 实现，`gfx/model_test.go` 9 个用例，`testdata/model_demo.js` 可跑）。
> 定位：给**受控组件**加一条 `model` 指令，等价于 Vue 的 `v-model`。
> `For / Show / Switch / Match` 的 API **一个字都没改**（理由见 §5）。

---

## 1. 要解决的问题

Gox 的受控组件是**完全受控**的：`<input>` 显示的文本只从 `Props["value"]` 读，打字只是
把新文本算出来然后派发 `onInput` —— 值真正落地要等脚本写回 signal。于是"让一个输入框
能用"至少是两件事：

```js
<input value={() => draft()} onInput={(e) => setDraft(e.value)} />
```

两件事都能写错，而且**都不报错**：

| 写错的样子 | 现象 | 反馈 |
|---|---|---|
| `value={draft()}`（漏了函数） | 拿到的是第一帧快照，signal 之后再变也不更新 | 无 |
| 忘了写 `onInput` | 编辑结果无处可去，输入框"打不进字" | 无 |
| `value={draft}`（信号裸传） | **其实是对的**，但和上面那行看起来只差一对括号 | — |

第三行是关键：**对与错只差一对括号**，而三种写法都合法、都渲染。这类"语法近似"是
DX 差的根因 —— 不是不会写，是写错了没人告诉你。

`<checkbox>` / `<switch>` 更别扭：它们的 `onClick` **没有载荷**，新值只能由当前值取反，
于是"一个开关"要写三段：

```js
<checkbox checked={() => agree()} onClick={() => setAgree(!agree())} />
```

`<radio>` 组要手写互斥（每个都要写"选中时写我 + 不选中时写别人"）。

## 2. 接口

一条指令，读写一起给：

```jsx
<input model={draft} />
```

`model` 收两种东西：

| 形态 | 写法 | 用途 |
|---|---|---|
| **signal** | `model={draft}` | `createSignal` 的 getter。它**自带 setter**（`draft.set`），所以一个值就能同时给出读与写 |
| **二元组** | `model={[get, set]}` | 自定义来源：派生值 / 嵌套字段 / 别人的存储。形状与 `createSignal` 的返回值一致，`const [v, setV] = ...` 原样转手即可 |

实现上 `model` 是 **`h()` 里的一次 props 改写**：补上该标签的读方向 prop（函数值 ⇒ 走原有
的"响应式 prop"接线）与写方向事件（`on*` ⇒ 走原有的"事件回调"接线）。`input.go` /
`textarea.go` / `slider.go` / `select.go` / `raster.go` / `layout.go` **一行都没改**。

## 3. 语义表（每个标签一行，没有例外）

| 标签 | 读方向 | 写方向事件 | 写进去的值 |
|---|---|---|---|
| `input` · `textarea` | `value` | `onInput({value})` | 字符串 |
| `slider` | `value` | `onInput({value})` | **数字**（控件给什么就是什么，不做转换） |
| `select` | `value` | `onChange({value})` | 字符串 |
| `checkbox` · `switch` | `checked` | `onClick()`（无载荷） | 布尔，**写入 = 当前值取反** |
| `radio` | `checked`（**派生**：`model() === value`） | `onClick()` | 属性 `value` 原样写进 model |

两条推论：

- `radio` 的互斥不是"两边各写一遍取反"，而是**共用一个 model 的必然结果**
  （`<radio model={plan} value="free"/>` + `<radio model={plan} value="pro"/>`）；
- `slider` 不做 `"3"` → `3` 这种类型转换。想转换就自己在 `[get, set]` 里转 ——
  内核不猜意图（`TestModelSliderAndSelect` 用 `typeof` 把这条钉住了）。

## 4. 三条规则（都不静默）

1. **冲突**：同一标签同时给了 `model` 与 `value`/`checked` ⇒ **`model` 覆盖**并警告一次。
   两个值来源同时存在是笔误，不是配置。
2. **合成**：同时给了 `onInput`/`onChange`/`onClick` ⇒ **两个都跑**，model 的写回在先，
   脚本的处理器在后。于是"存进 signal + 顺便做个副作用"是合法组合，而不是被吞掉：
   ```jsx
   <input model={q} onInput={(e) => search(e.value)} />
   ```
3. **降级不炸**（但都打 stderr 警告，消息里说清为什么）：

   | 传了 | 结果 |
   |---|---|
   | 没有 setter 的函数（`createMemo` / 手写取值函数） | 只接读方向，写方向不生效 |
   | 标量（`model={draft()}`） | 当静态值接上，界面照旧渲染，只是写不回去 |
   | 不认识的形状 / 不支持的标签（如 `<row model={x}>`） | 忽略这条指令 |

   这不是"宽容"，是**与内核一贯的降级口径一致**（一个坏 props 不该端掉整个窗口），
   区别在于这次全部出声。警告做了去重：`h()` 在每次重建时都会跑（列表行的渲染函数、
   响应式分支换代），不去重会刷屏。

## 5. 为什么不动 `For / Show / Switch / Match`

这三个的现有 API 已经是**声明的 1:1 翻译**，没有"两种差不多的写法"：

```jsx
<For each={() => rows()} key={(r) => r.id} stable fallback={<text>空</text>}>{(row) => <Row row={row} />}</For>
<Show when={() => open()} fallback={<text>隐藏中</text>}>…</Show>
<Switch fallback={<text>未知</text>}>
  <Match when={() => phase() === "loading"}>…</Match>
</Switch>
```

它们的问题不在写法，而在**文档之前没人知道 `each` 要传取值函数**（见
`docs/gui-patterns.md` §9 的三条语义）。给它们再加一层糖（例如 `<Match is="loading" on={phase}>`）
只会引入**第二套调用约定** —— 而"同一组组件里并存三种传法"正是这次要消灭的东西。
所以这里的取舍是：**受控组件加糖（痛点明确、收益可量化），控制流不加糖（已经很薄）。**

顺带一条已经可用、但很少人知道的简写：**signal 本身就是函数**，所以任何"要取值函数"的
位置都可以裸传 signal，不用包箭头：

```jsx
<input model={draft} />      {/* 读写一体 */}
<text>{draft}</text>         {/* 响应式子节点 */}
<For each={rows}>…</For>     {/* 等价于 each={() => rows()} */}
<Show when={open}>…</Show>   {/* 等价于 when={() => open()} */}
```

`model={draft}` 正是这条简写的自然延伸 —— 既然 signal 能当取值函数传遍全场，那它也该能
把自己携带的 setter 一起带上。

## 6. 与 Vue 的对照

| Vue | Gox | 说明 |
|---|---|---|
| `v-model="draft"` | `model={draft}` | 直接对应 |
| `v-model="user.name"` | `model={[() => user().name, (v) => setUser({...user(), name: v})]}` | Gox 的 signal 不可变更新，嵌套字段要显式给 setter（也可以先 `createMemo` 出子 signal） |
| `v-model.number` | 用 `[get, set]` 自己 `Number(v)` | 不做隐式转换（`slider` 例外：它本来就给数字） |
| `v-model.lazy` | 用 `onInput` 观察 + 自己 commit | model 只跟控件的事件口径 |
| `v-model` + `@input` 同时写 | `model` + `onInput`（**合成，都跑**） | 与 Vue 行为一致 |
| 自定义组件 `v-model` | `<input model={p.model} />` 原样透传 | setter 长在 getter 身上，跟着函数值走，不需要第二根线 |

## 7. 示例集

### 7.1 迁移对照（一个输入框）

```jsx
{/* 旧: 两个 prop, 漏了 onInput 就静默失效 */}
<input value={() => draft()} onInput={(e) => setDraft(e.value)} />

{/* 新: 一条指令 */}
<input model={draft} />
```

### 7.2 每个受控组件

```jsx
<input model={name} />
<textarea rows={3} model={bio} />
<slider min={0} max={100} model={volume} />          {/* volume 是数字 */}
<select options={cities} model={city} />
<checkbox model={agree} />                            {/* 点击自动取反 */}
<switch model={dark} />
<radio model={plan} value="free" />                   {/* 选中时 plan = "free" */}
<radio model={plan} value="pro" />
<input model={[() => profile().nick, (v) => setProfile({ nick: v })]} />   {/* 自定义来源 */}
```

### 7.3 反例（都会打警告，不会静默）

```jsx
<input model={draft()} />                  {/* 标量: 只读, 界面仍是 draft() 的值 */}
<input model={createMemo(() => draft())} />{/* 没有 setter: 只读 */}
<input model={draft} value={() => "x"} />  {/* 冲突: model 为准 + 警告 */}
<row model={draft}></row>                  {/* 不支持受控的标签: 忽略 + 警告 */}
```

### 7.4 副作用组合

```jsx
<input model={q} onInput={(e) => search(e.value)} />   {/* 存值 + 搜索, 两个都跑 */}
```

### 7.5 与控制流共存（用法完全不变）

```jsx
<Show when={open}>
  <input model={draft} />
</Show>
<Switch fallback={<text>未知状态</text>}>
  <Match when={() => phase() === "editing"}><input model={title} /></Match>
  <Match when={() => phase() === "saved"}><text>已保存: {(title)}</text></Match>
</Switch>
```

完整可跑版本：`testdata/model_demo.js`（`go run . testdata/model_demo.js`），
其中第 ③ 行 `state` 是全部绑定的投影 —— 每个控件点一下、敲一下都能在那一行看到。

## 8. 边界与已知限制

- **只覆盖内置受控组件**（上面表里那 7 个标签）。自定义组件要么透传 `model`，要么自己
  按 `get/set` 实现。
- **`radio` 的 `value` 必须是字面量**：它是"选中时写回什么"的载荷，写成函数会让比较恒不
  相等（会警告）。
- **`checkbox/switch` 的 model 应当装布尔**：写入是按"当前值取反"算的，装字符串会得到
  `true/false` 而不是 `"a"/"b"`。
- **不做输入节流 / 防抖**：`onInput` 每个按键一次，要合并自己 `setTimeout`（与
  `docs/gui-patterns.md` §3 的 resize 同一立场：内核不猜业务频率）。
- **不做 `<form>` 级的批量绑定**：值的真身在 signal 里，提交时直接读即可。

## 9. 验证

`gfx/model_test.go`（9 个用例，全链路真事件）：

| 用例 | 钉住的语义 |
|---|---|
| `TestModelInputBothDirections` | 敲键写回 + 外部改 signal 后显示跟随（读方向不是快照） |
| `TestModelCheckboxSwitchRadio` | 取反写回；radio 派生 `checked` 与互斥 |
| `TestModelSliderAndSelect` | slider 写回是 **number**；select 写回字符串 |
| `TestModelPairSourceAndUserHandler` | `[get, set]` 自定义来源；model 与脚本 `onInput` 都跑 |
| `TestModelMisuseWarnsAndDegrades` | 三类误用各有警告，且界面照旧渲染 |
| `TestModelSignalCarriesSetter` | `getter.set` 这条凭据本身（`gx/solid` 的契约） |
| `TestModelDemoScript` | 演示脚本：8 类绑定各点一次 + 不溢出窗口 |

## 10. 相关文档

- `docs/gui-guide.md` §4 元素表（`model` 一行）与 §6 受控组件（用法速查）
- `docs/gui-patterns.md` §9 视图（`For / Show / Switch` 的语义与边界，本次未改）
- `gfx/view.go` 文件头（控制流的完整设计理由）、`gfx/model.go` 文件头（本指令的完整设计理由）
