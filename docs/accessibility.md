# 无障碍与键盘导航规范（T10）

> 一句话：**纯键盘能完成"填表 → 提交 → 关弹窗"的全流程**，并且每个组件的键盘语义
> 都写在这份文档里、由测试锁住。内核不做读屏桥（见 §8），但 role / name / 焦点链
> 是**可读的**，脚本能把它们取出来做自检。

本文是 T10 的落地记录与验收清单。落地代码集中在 `gfx/a11y.go`（一层，全组件共用），
组件各自的键盘语义散在 `gfx/select.go` / `gfx/datepicker.go` / `gfx/colorpicker.go` /
`gfx/input.go` / `gfx/tabs.go` 等处，但**分派点只有一个**（见 §3.4）。

---

## 1. 范围：做什么、不做什么

| 做 | 不做 |
| --- | --- |
| 焦点注册表：谁进 Tab 序、按什么顺序进 | 读屏桥（Windows UIA / macOS AX / Android AccessibilityService） |
| Tab / Shift+Tab 遍历、循环绕回、弹层焦点陷阱 | 焦点在**窗口之间**迁移（多窗口各有一份独立焦点） |
| 组合控件（radio 组 / slider / rating / tabs / pagination）的方向键语义 | 文本编辑的高级键（`Ctrl+←` 按词移动、Home/End 行首行尾、选区） |
| Enter / Space 激活（与鼠标点击同一条出口） | 快捷键自定义 / 全局热键注册（那是 `onKeyDown` 与菜单的事） |
| 隐式 role + 显式覆盖、`aria-*` 拼写告警 | `aria-owns` / `aria-activedescendant` 这类**关系型**属性的语义执行 |
| 焦点悬空自动校正 | 屏幕阅读器朗读文本顺序的显式控制 |

**为什么不做读屏桥**：三平台三套 API，工作量与"零 cgo"的桌面承诺直接冲突；而
组件库这一期要解决的是"**键盘用户能不能把事做完**"——这是纯计算，能在 CI 里断言。
读屏桥留给宿主（移动壳可以接系统无障碍树，接口位置见 §8）。

---

## 2. 焦点模型

焦点是**两个同步的字段**：`app.focused`（分发键盘事件用）与 `GuiNode.focused`
（绘制用）。点击获焦、输入框光标、虚线焦点框的行为与 T10 之前**完全一致**；
T10 只把"谁能拿焦点、怎么用键盘换焦点"从"只有鼠标能改"扩到"键盘也能改"。

### 2.1 `focusable`：谁进 Tab 序

| 值 | 含义 |
| --- | --- |
| 不写 | 按**标签表**决定：表单控件与按钮进序，容器类（`column` / `row` / `view` / `rect` / 弹层）不进 |
| `focusable={true}` | 强制进序（给容器 / 图标用） |
| `focusable={false}` | 强制不进序（把"用 Tab 跳过这个搜索框"这类需求做掉） |

标签表 `a11yNativeFocusable`（不写 prop 也进序）：
`button` `checkbox` `radio` `switch` `slider` `input` `search` `textarea` `select`
`rating` `tabs` `pagination` `datepicker` `colorpicker` `upload`。

::: tip 容器默认不进序是刻意的
给一个纯布局盒子加 Tab 停留点，会让键盘用户为到达真正的控件多按好几次空 Tab。
这是无障碍实现里最常见的自伤，所以默认值选"不进"。
:::

**判据只看节点自身**，不沿祖先链继承：内置的 `slot` / `view` 是"布局透明"的
单子容器，如果可聚焦性沿链继承，任何包了一层的容器都会隐式变成可被 Tab 停下的
东西 —— 而它们在画面上与子节点完全重合，用户看到的是"Tab 停在一个空白上"。

### 2.2 `tabIndex`：什么顺序

与 DOM 一致：

| 值 | 行为 |
| --- | --- |
| `> 0` | 按数值**升序**排在最前（"先跳到我这里"） |
| `0`（缺省） | 按**树序**（也就是绘制序） |
| `< 0` | 不进 Tab 序，但仍可被程序聚焦（`focusNode()`） |

负数一律按 `-1` 处理（DOM 只认 `-1`，更小的值当 `-1` 是各引擎的实际行为）。

### 2.3 被排除的节点

- `disabled`（沿祖先链继承）：禁用子树既不响应事件也不进序；
- `hidden` / `aria-hidden="true"`（两种都认：字符串 `"true"` 与布尔 `true`）；
- **未布局**（`Box` 宽或高为 0）：已关闭弹层的内部节点、`tabs` 的非激活页；
- **与窗口矩形不相交**：滚出视口的行不该能被 Tab 到（否则键盘用户"跳进看不见的地方"）；
- **弹层内部结构**：`select-popup` / `menu-popup` / 日历格 / 色板格不进 Tab 序 ——
  它们是"方向键在里面走"的复合控件内部结构（与浏览器里 listbox 的选项不进 Tab 序
  完全一致）。让它们各自成一个停留点，会把一个下拉变成十几次 Tab。

### 2.4 焦点校正（每轮事件末尾）

焦点是个**可悬空的指针**：弹层被打开（焦点被盖在遮罩下）、弹层关闭（焦点节点整支
不绘制）、条件渲染摘掉节点、脚本把控件 `disabled` —— 这些都不是"点了别的地方"，
内核没有任何事件可挂钩。于是焦点会留在一个不可见/不可用的节点上，症状是
"Tab 从这里继续走，但焦点框看不见"。

`a11yReconcileFocus` 在每轮事件处理末尾检查"焦点是否还站得住"（可见 / 未被弹层盖住 /
未禁用），站不住就把焦点交给当前遍历序里的第一个可聚焦节点；遍历序为空则交还
`nil`（焦点回到根）。**只在焦点非空且失效时才做 O(树) 的计算**，绝大多数事件轮次
只付一次 O(深度) 的判定。

### 2.5 焦点框

`drawFocusRing` 给焦点节点画 2px 虚线框（`colorFocusRing`），内缩 1px 以免盖住控件
自己的边框。以下情况**不画**，每条都有具体理由：

| 情况 | 为什么 |
| --- | --- |
| 焦点在根节点上 | 点空白处焦点回根，给整窗画一圈虚线没有意义 |
| 焦点在弹层自身上 | 点遮罩会把焦点给 `dialog`，框会绕着整个窗口 |
| 焦点在**已关闭**弹层的子树里 | 节点仍留在树上（`open=false`），整支都不绘制，框不能自己冒出来 |
| 焦点被打开的弹层盖住 | 框画在 `Draw` 之后会浮在遮罩上，成了"透过遮罩的幽灵框" |
| 节点被禁用 | 禁用控件不该有焦点反馈 |

演示脚本里想拍"没有焦点框"的画面，在窗口根上写 `hideFocusRing={true}`。

---

## 3. 键盘

### 3.1 Tab / Shift+Tab

- 在遍历序里前进 / 后退，到两端**循环绕回**；
- 焦点不在序里（含 `nil`）时：Tab 进序首，Shift+Tab 进序末；
- **遍历序为空时不消费**这个键（按键照旧给脚本）—— 消费纪律见 §3.4；
- 焦点在输入框里按 Tab 也是"离开这个字段"，不插入制表符（制表符不在 v1 范围）；
- 展开中的下拉会**先收起**：Tab 是"离开这个字段"的动作，弹层跟着走会让人以为
  焦点还在下拉里。

### 3.2 焦点陷阱（弹层）

`a11yFocusScope` 返回"最上层可见的模态弹层（`dialog` / `drawer`）"，没有就返回整棵树。
遍历序按这个范围算，于是弹层打开时 Tab 绕来绕去都出不去 —— **不必额外维护陷阱栈**；
弹层关闭后节点仍在树上（仅 `open=false`），天然被 §2.3 排除。

### 3.3 方向键：按组件分工

| 组件 | 按键 | 语义 |
| --- | --- | --- |
| `radio`（同组） | `←` `↑` / `→` `↓` | 组内移动 + **选中跟着焦点走**（ARIA 约定） |
| `slider` | `←` `↓` / `→` `↑` | ± 一个 `step` |
| `rating` | `←` / `→` | ∓ / ± 一颗星 |
| `tabs` | `←` / `→` | 上一 / 下一页 |
| `pagination` | `←` / `→` | 上一 / 下一页 |
| `select` | `↑` `↓` | 移动高亮（环绕）；未展开时先展开 |
| `datepicker` | `←` `→` / `↑` `↓` / `PageUp` `PageDown` / `Home` `End` | 前后一天 / 一周 / 一月 / 本月首末日 |
| `colorpicker` | `←` `→` / `↑` `↓` / `Home` `End` | 前后一格 / 上下**一列** / 首末格 |

两条通用约定：

1. **带 `Ctrl` / `Alt` 的一律不碰**。那是快捷键的地盘（`Ctrl+←` 将来是输入框里
   "按词移动光标"），内核的 `handleShortcut` 已经排在更前面；这里再拦一道只是让
   "没注册成快捷键的 Ctrl+方向键"也不会误改控件值。
2. **边界行为看维度**：一维组（radio / tabs / pagination / select）**环绕**，
   二维组（datepicker 的日期、colorpicker 的色板）**停住** —— 色板上从第一行按 `↑`
   绕到最后一行与直觉相反。

radio 组采用 **roving tabindex**：整组在 Tab 序里只占**一个**停留点（停留点是"当前
选中的那个"，没有选中的就是组里第一个），进组之后用方向键在组内走。组的分组键是
`name` prop；没写 `name` 时按"同一个父节点"分组（`parent:%p`）—— 这样"两个 radio
并排当开关用"这种最简用法不用被迫起名。

### 3.4 Enter / Space 激活，以及"什么时候消费按键"

Enter / Space 的"激活"**等价于用鼠标点一下这个控件**：走同一个出口（派发节点自己的
`onClick`）。`checkbox` / `switch` 的取反、`radio` 的选中、`select` `datepicker`
`colorpicker` 的展开、`upload` 的弹文件框，全挂在 `onClick` 上，所以**不必为每个
控件写一份"键盘版"逻辑** —— 写两份的结局必然是某天只有鼠标那半被改动。

**能不能被激活，看 role 而不是"标签能不能点"**：浏览器里 Enter 触发 click 的是
`button` / `checkbox` / `radio` 这类原生控件，一个普通的 `<div onclick>` 按 Enter
什么都不会发生。可激活 role 集合：
`button` `checkbox` `radio` `switch` `link` `menuitem` `menuitemcheckbox`
`menuitemradio` `option` `tab` `treeitem` `combobox` `spinbutton`（+ 标签 `upload`，
它的隐式 role 是 `group`，而"一组文件"本身不该激活）。

于是：

- `<rect onClick>` 这类自定义可点区域按 Enter **不会**被激活 —— 想拿到这个语义就
  显式写 `role="button"`；
- 文本框（`textbox` / `searchbox`）不在表里，所以"输入框上挂了 `onClick` 时按 Enter
  顺手触发一次"这种意外不会发生 —— 单行输入框里 Enter 是"提交 / 换行"的语义位。

**带 Ctrl / Alt 的组合键在整条链路上都不碰**：输入框 → 字段类（select / picker /
upload）→ Tab → 表单提交 → 全局快捷键 → 菜单 → Esc 兜底 → 无障碍层 → 脚本。
每一环都是"**只在真的做了事时才消费**，没做事就返回 false 继续往下传"。

### 3.5 回车提交表单

焦点在 `input` / `search` 里按 Enter = 提交所在的 `<form>`（浏览器里 `<input>` 回车
提交表单的同一套直觉）。判据（`formShouldSubmit`）从焦点往上走：

- 先遇到 `form` ⇒ 提交（且路径上必须出现过可编辑字段，否则一个纯展示的 form 会把
  所有 Enter 都吃掉）；
- 先遇到可激活控件（按钮 / 复选框 / 下拉 …）⇒ 回车归它，不提交。

所以"焦点在按钮上按 Enter"是按下这个按钮，"在输入框里按 Enter"是提交整张表单。

---

## 4. aria 语义

### 4.1 隐式 role

每个内置标签都有一行默认 role（表在 `a11yImplicitRole`）。取值依据：HTML 隐式 role
优先，HTML 没有对应物的组件取 ARIA 里语义最接近的。

| 标签 | role | | 标签 | role |
| --- | --- | --- | --- | --- |
| `button` | `button` | | `tabs` / `tab` | `tablist` / `tab` |
| `checkbox` | `checkbox` | | `pagination` | `navigation` |
| `radio` | `radio` | | `progress` | `progressbar` |
| `switch` | `switch` | | `spinner` | `progressbar` |
| `slider` | `slider` | | `dialog` / `drawer` | `dialog` |
| `input` | `textbox` | | `toast` / `badge` / `tag` | `status` |
| `search` | `searchbox` | | `alert` | `alert` |
| `textarea` | `textbox` | | `tooltip` | `tooltip` |
| `select` | `combobox` | | `menubar` / `menu` / `menuitem` | 同名 |
| `datepicker` / `colorpicker` | `combobox` | | `table` / `tree` | 同名 |
| `rating` | `slider` | | `list-item` | `option` |
| `upload` | `group` | | `icon` / `avatar` | `img` |
| `form` | `form` | | `label` | `label` |
| `scroll` | `region` | | `empty` / `skeleton` | `status` |

显式 `role` prop 永远覆盖隐式值；取值必须在 ARIA 1.2 的 role 白名单里，写错会打印
**一次**告警并回落到隐式值 —— 而不是静默变成一个无名容器。
用 `focusOrder()` / `roles()` 取表，别在文档里手抄（`roles()` 就是直接从内核表出来的）。

### 4.2 无障碍名（accessible name）

取值顺序照 ARIA 的 "author > content" 分级：

```
aria-label  →  title  →  文本内容（TextContent）  →  placeholder（字段类）
```

空串表示"没有可读名字"，`focusOrder()` 会**原样报出来**，而不是编一个（"button 3"）。
让缺失暴露在外面，才有人去补 `aria-label`。
`aria-description` / `description` prop 作为附加说明单独给出。

### 4.3 `aria-*` 属性

内核认 ARIA 1.2 的 `aria-*` 全集（见 `a11yKnownAriaProps`），但不执行它们的语义
（除 `aria-hidden` 参与"是否进序"的判定）。表里覆盖全集而不是"内核用到的子集"：
**要挡的是拼错，不是"用得多"**。

`aria-lable="删除"` 这种拼写错既不报错也不会生效，只会让无障碍名静默丢失 —— 而丢失的
名字恰恰是读屏用户唯一能拿到的东西。所以 `h()` 里会校验一次，**每个拼错的名字只告警
一次**（去重表），并带上标签名与正确写法建议。

---

## 5. `gx/a11y` 模块

```js
import { focusOrder, focusNode, focusNext, focusPrev, roles } from "gx/a11y";
```

| API | 返回 | 用途 |
| --- | --- | --- |
| `focusOrder()` | 数组 | 当前窗口的 Tab 遍历序快照（每项见下） |
| `focusNode(el)` | boolean | 程序化聚焦（等价 DOM 的 `el.focus()`）；节点不在树里 / 已禁用返回 `false` |
| `focusNext()` / `focusPrev()` | boolean | 与 Tab / Shift+Tab **完全同一条路径**（含绕回与弹层范围限制） |
| `roles()` | 数组 | 隐式 role 表（`{tag, role, keyHint?}`），文档与画廊表直接用 |

`focusOrder()` 每一项的形状：

```js
{
  role: "textbox",            // 显式 role prop 优先, 否则隐式值
  name: "手机号",              // aria-label > title > 文本 > placeholder
  tag: "input",               // 元素标签
  tabIndex: 0,                // 负数 = 不进序 (仍可能被程序聚焦)
  disabled: false,            // 沿祖先链继承
  focused: true,              // 是不是当前焦点
  description: "…",           // 可选: aria-description / description
  keyHint: "方向键按一步（step）增减",  // 可选: 这个控件的键盘用法
  box: { x, y, width, height }   // 当前帧的几何 (窗口坐标)
}
```

用途有三个（都**不是**"给读屏用"）：

1. **回归**：本文档的验收清单（§7）在测试里就是"按 Tab 若干次 → 断言当前焦点是谁"；
2. **自检**：`focusOrder()` 里出现 `name` 为空的 button / 图片，就是漏了 `aria-label`
   —— 比"读屏用户反馈读不出东西"早得多；
3. **官网画廊**：a11y 面板直接列出当前示例的焦点链，比截图更能说明问题。

报的是**当前帧**的几何，所以调用点要保证窗口已经渲染过一帧。

---

## 6. 写脚本时的动手约定

| 想做的事 | 怎么写 |
| --- | --- |
| 让容器 / 图标能被 Tab 到 | `focusable={true}` |
| 用 Tab 跳过某个控件 | `focusable={false}` |
| 让自定义可点区域支持 Enter | `role="button" focusable`（光有 `onClick` 不够） |
| 调 Tab 顺序 | `tabIndex={1,2,3…}`（正数整体排在自然序之前） |
| 把控件排除出序但保留程序聚焦 | `tabIndex={-1}` |
| 图标按钮要有名字 | `aria-label="删除"` 或 `title="删除"` |
| 给读屏一段额外说明 | `aria-description="…"` |
| 拍"无焦点框"的截图 | 窗口根写 `hideFocusRing={true}` |

---

## 7. 验收清单

### 7.1 全流程（T10 的 DoD）

**用键盘（不碰鼠标）完成：填表 → 提交 → 关弹窗。** 逐步断言：

| # | 操作 | 期望 |
| --- | --- | --- |
| 1 | 启动后按 `Tab` | 焦点进入第一个可聚焦控件，焦点框可见 |
| 2 | 继续按 `Tab` | 焦点按 树序 / `tabIndex` 顺序前进，**不漏、不重、不跳进不可见的节点** |
| 3 | 在末尾再按 `Tab` | 绕回第一个控件（不"卡死"在最后一个） |
| 4 | 焦点到 `input`，直接打字 | 文字进入输入框（`onInput` 派发，值受控回写） |
| 5 | 焦点到 `select` / `datepicker` / `colorpicker`，按 `Enter` | 弹层展开 |
| 6 | 按 `↑` `↓`（或 `←` `→`） | 高亮 / 光标在弹层里移动 |
| 7 | 按 `Enter` | 选中并收起弹层，`onChange` 派发，**焦点回到字段本身** |
| 8 | 在弹层展开时按 `Esc` | 收起弹层，值不变 |
| 9 | 焦点到 `radio` 组，按 `→` | 组内移到下一个并选中（`onChange` 派发一次） |
| 10 | 焦点到 `submit` 按钮，按 `Enter` / `Space` | 按钮被触发（不是提交表单的 Enter 语义） |
| 11 | 焦点在 `input` 里按 `Enter` | 提交所在的 `<form>`，`onSubmit({values})` 派发 |
| 12 | Trigger 一个 `dialog`（按钮或脚本） | 弹层打开，遮罩可见 |
| 13 | 在 `dialog` 里反复按 `Tab` | 焦点在弹层内循环，**永远不会跑到弹层外的控件上** |
| 14 | 按 `Esc` | 弹层关闭 |
| 15 | 关闭后按 `Tab` | 焦点自动回到弹层外的第一个可聚焦控件（焦点校正生效） |

回归位置：`gfx/a11y_test.go`（焦点序 / Tab 循环 / 焦点陷阱 / 校正 / 方向键 / aria）、
`gfx/a11y_test.go` 的 `TestA11yFormDemoKeyboardWalkthrough`（**就是上面这张表**：
在演示脚本上按声明顺序走完每一站并断言 role + 无障碍名）、`gfx/form_test.go`
（回车提交与取值）、`gfx/datepicker_test.go` / `gfx/colorpicker_test.go` /
`gfx/upload_test.go`（三个新控件的键盘语义）。

人工走查用 `testdata/a11y_form_demo.js`（`./gox testdata/a11y_form_demo.js`）：
一张表单 + 一个模态弹窗，第 ② 行是提交结果、第 ③ 行是 `focusOrder()` 的投影 ——
走一遍就能看到"焦点现在在哪、遍历序几项、这个控件缺不缺名字"。

### 7.2 组件作者自检（加新组件时逐条过）

- [ ] 标签进了 `a11yNativeFocusable`（如果它天然该被 Tab 到），或明确说明为什么不该；
- [ ] 标签进了 `a11yImplicitRole`（否则 role 落空，无障碍层把它当无名容器）；
- [ ] 有 `onClick` 且"点击=主要动作"的控件，确认它的 role 在 `a11yActivatableRoles`
      里（不在的话 Enter 激活不到它）；
- [ ] 复合控件的**内部结构**不进 Tab 序（选项 / 格子 / 行），方向键在内部走；
- [ ] 加了 `a11yKeyHint` 文案（画廊面板与文档表都读它）；
- [ ] 键盘能完成它支持的所有鼠标动作（点选、展开、收起、切换、取消）；
- [ ] 弹层类的组件：`Esc` 能收起、点外部能收起、收起时焦点**回到触发它的字段**；
- [ ] 缺名字的控件（图标按钮、只有图形的按钮）能通过 `aria-label` 命名；
- [ ] 跑 `scripts/check-registries.py`（四处注册表同步）；
- [ ] 在 `docs/accessibility.md` §3 的键盘表里加一行，并在官网组件页写明键盘用法。

### 7.3 已知边界（写清楚免得被当 bug）

| 现象 | 说明 |
| --- | --- |
| 读屏软件读不出任何东西 | 内核**没有**读屏桥（§1）。role / name 只是"可读"，朗读由宿主实现 |
| 焦点不能在窗口之间跳 | 多窗口各有独立焦点；`focusNext()` 只走当前窗口 |
| 输入框里没有 `Home` / `End` / 选区 / 撤销 | 文本编辑的键位不在一期范围（`Home`/`End` 只在日历里生效） |
| `radio` 不带 `name` 时"同父分组"可能过宽 | 一个容器里放两组单选会串成一组；给 `name` 即可 |
| `tabs` 的非激活页里的控件不能被 Tab | 那正是 keep-alive 的语义（切走再切回来状态还在，但不参与 Tab） |
| 滚出视口的控件不能被 Tab | 有意的：不让键盘用户跳进看不见的地方。需要时把 `scroll` 滚到它 |

---

## 8. 与宿主（移动壳）的关系

内核把"可聚焦节点 + role + name + 几何"这四样东西算全了，但**没有**把它们推给平台的
无障碍树。移动壳想接系统读屏时，接口位置就是 `gx/a11y` 的 `focusOrder()` —— 进程内
取一次快照（含 `box`），再翻成平台的节点树即可，不必再抄一遍"谁可聚焦"的规则。

这也是把这一层放在内核而不是各组件里的原因：**规则只有一份**，宿主与脚本读到的
是同一个答案。
