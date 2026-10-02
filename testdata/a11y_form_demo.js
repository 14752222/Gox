// T10 键盘走查演示: 全程不碰鼠标走完"填表 → 提交 → 关弹窗"。
// 运行: ./gox testdata/a11y_form_demo.js
//
// 走查步骤 (每一步的结果都落在第 ② / ③ 行上, 不用看代码就知道对不对):
//   1. 启动后按 Tab —— 焦点进第一个字段 (姓名), 焦点框可见; 再按 Tab 依次前进,
//      到末尾**绕回**开头, Shift+Tab 反向; 顺序与声明顺序一致, 不漏不重;
//   2. 直接打字写进当前字段 (姓名的值当场显示在行尾);
//   3. 下拉 / 日历 / 色板 / 附件按 Enter 打开: 下拉与日历用方向键选择、Enter 选中
//      (焦点回到字段自己身上), 中途 Esc 放弃; 单选组只占一个 Tab 停留点, 进组后
//      ← / → 换选项;
//   4. 焦点在姓名框里按 Enter —— **提交整张表单**, 所有带 name 的字段值出现在第 ② 行;
//   5. 焦点挪到"打开确认弹窗"按 Enter —— 弹层打开; 在弹层里反复 Tab **出不去**;
//      Esc 关闭后焦点自动落回外部 (焦点校正);
//   6. 第 ③ 行是 `gx/a11y` 的 `focusOrder()` 自检投影: `name` 为空并带 ⚠ 的项,
//      就是漏了 `aria-label` 的控件 —— 比"读屏用户反馈读不出东西"早得多。
//
// 无障碍名一律显式给 `aria-label`: 内核**不给** `<label>` 与字段做隐式关联
// (没有 for/id 那套), 所以字段的可读名字只可能来自 aria-label / title / placeholder。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";
import { focusOrder } from "gx/a11y";

// ===== 状态: 界面全部派生自这几个 signal, 不存第二份 =====

const [name, setName] = createSignal("");
const [note, setNote] = createSignal("");
const [city, setCity] = createSignal("beijing");
const [due, setDue] = createSignal("");
const [brand, setBrand] = createSignal("#2f80ed");
const [volume, setVolume] = createSignal(40);
const [files, setFiles] = createSignal([]);
const [agree, setAgree] = createSignal(false);
const [notify, setNotify] = createSignal(true);
const [plan, setPlan] = createSignal("free");
const [submitted, setSubmitted] = createSignal("(还没提交)");
const [dialogOpen, setDialogOpen] = createSignal(false);
// focusOrder() 是**纯读数** (不订阅任何 signal), 所以不能直接在文本 effect 里
// 裸调它 —— 那样只会在挂载时求值一次, 之后焦点换了它也不动 (正好踩中"快照"
// 那个经典坑)。这里用一个 focus 事件驱动的计数器当依赖, 见下面的 onFocus。
const [focusTick, setFocusTick] = createSignal(0);

const cities = ["beijing", "shanghai", "shenzhen"];
const brandPalette = ["#2f80ed", "#27ae60", "#c0392b", "#8e44ad"];

// Row 是"标签 + 控件 + 当前值"的一行: 键盘走查时值变了当场看得见。
const Row = (p, ...kids) => (
  <row gap={10} alignItems="center">
    <label width={64} align="right">{p.label}</label>
    {kids}
  </row>
);

// ③ 那一行的内容: 从 focusOrder() 里挑出当前焦点, 顺带把缺名字的控件标出来。
// 报的是**当前帧**的几何, 所以它只在文本 effect 里读 (每次重绘都会重算一遍)。
const focusLine = () => {
  focusTick(); // 唯一依赖: 焦点变化时 onFocus 会把它 +1
  const order = focusOrder();
  let cur = null;
  for (let i = 0; i < order.length; i++) {
    if (order[i].focused) cur = order[i];
  }
  if (cur === null) {
    return "焦点: (无) · 遍历序 " + order.length + " 项 —— 按 Tab 开始走查";
  }
  const warn = cur.name === "" ? "  ⚠ 缺 aria-label" : "";
  const hint = cur.keyHint ? " · " + cur.keyHint : "";
  return "焦点: " + cur.role + " \"" + cur.name + "\"" + warn + " · 遍历序 " + order.length + " 项" + hint;
};

// 提交结果的排版只有一份: 内核收上来的 values 与按钮自己读 signal 的快照共用它。
const formatted = (v) => {
  return "name=" + v.name + " · city=" + v.city + " · due=" + v.due +
    " · brand=" + v.brand + " · volume=" + v.volume +
    " · attach=" + (v.attach ? v.attach.length : 0) + " · agree=" + v.agree +
    " · notify=" + v.notify + " · plan=" + v.plan;
};

// snapshot 是"此刻的字段值"—— 给按钮用。它与内核 formValues() 的收集口径同源
// (只收带 name 的字段), 但来源不同: 这个从 signal 读, 内核从受控 prop 读。
const snapshot = () => ({
  name: name(), city: city(), due: due(), brand: brand(), volume: volume(),
  attach: files(), agree: agree(), notify: notify(), plan: plan(),
});

render(
  <window title="a11y — keyboard walkthrough" width={660} height={620}>
    <column gap={8} padding={14} onFocus={() => setFocusTick(t => t + 1)}>
      <text font={16}>键盘走查: 填表 → 提交 → 关弹窗</text>

      {/* ---- ① 一张表单: 每行都是"标签 + 字段 + 当前值" ---- */}
      <form gap={8} onSubmit={(e) => setSubmitted(formatted(e.values))}>
        <Row label="姓名">
          <input width={180} name="name" aria-label="姓名" placeholder="请输入姓名" model={name} />
          <text font={11} color="#2e7d32">{() => "= " + (name() === "" ? "(空)" : name())}</text>
        </Row>

        <Row label="备注">
          <textarea width={180} rows={2} name="note" aria-label="备注" model={note} />
          <text font={11} color="#2e7d32">{() => "= " + note().length + " 字符"}</text>
        </Row>

        <Row label="城市">
          <select width={180} name="city" aria-label="城市" options={cities} model={city} />
          <text font={11} color="#2e7d32">{() => "= " + city()}</text>
        </Row>

        <Row label="截止日">
          <datepicker width={180} name="due" aria-label="截止日" model={due} />
          <text font={11} color="#2e7d32">{() => "= " + (due() === "" ? "(空)" : due())}</text>
        </Row>

        <Row label="主题色">
          <colorpicker width={180} name="brand" aria-label="主题色" colors={brandPalette} columns={4} model={brand} />
          <text font={11} color="#2e7d32">{() => "= " + brand()}</text>
        </Row>

        <Row label="音量">
          <slider width={150} min={0} max={100} name="volume" aria-label="音量" model={volume} />
          <text font={11} color="#2e7d32">{() => "= " + volume()}</text>
        </Row>

        <Row label="附件">
          <upload width={180} name="attach" aria-label="附件" multiple model={files} />
          <text font={11} color="#2e7d32">{() => "= " + files().length + " 个文件"}</text>
        </Row>

        <Row label="同意条款">
          <checkbox name="agree" aria-label="同意条款" model={agree} />
          <text font={11} color="#2e7d32">{() => "= " + agree()}</text>
        </Row>

        <Row label="通知">
          <switch name="notify" aria-label="通知" model={notify} />
          <text font={11} color="#2e7d32">{() => "= " + notify()}</text>
        </Row>

        {/* 一组单选在 Tab 序里只占**一个**停留点 (roving tabindex), 进组后 ← / → 换项 */}
        <Row label="套餐">
          <radio name="plan" aria-label="套餐" value="free" model={plan} />
          <text font={11} color="#4a5560">free</text>
          <radio name="plan" aria-label="套餐" value="pro" model={plan} />
          <text font={11} color="#4a5560">pro</text>
          <text font={11} color="#2e7d32">{() => "= " + plan()}</text>
        </Row>

        <row gap={8} alignItems="center">
          <label width={64} align="right">操作</label>
          <button aria-label="提交" onClick={() => setSubmitted(formatted(snapshot()))}>提交</button>
          <button aria-label="打开确认弹窗" onClick={() => setDialogOpen(true)}>打开确认弹窗</button>
          <text font={11} color="#8a93a0">焦点在姓名框里按 Enter 才是"提交表单"</text>
        </row>
      </form>

      {/* ---- ② 提交结果 ---- */}
      <row gap={8} alignItems="center" background="#eef1f5" padding={8}>
        <text font={12} color="#4a5560" width={64}>② 提交</text>
        <text font={11} color="#3a4450" wrap width={560}>{() => submitted()}</text>
      </row>

      {/* ---- ③ 焦点自检 (focusOrder 的投影) ---- */}
      <row gap={8} alignItems="center" background="#eef1f5" padding={8}>
        <text font={12} color="#4a5560" width={64}>③ 焦点</text>
        <text font={11} color="#3a4450" wrap width={560}>{() => focusLine()}</text>
      </row>

      <text font={11} color="#8a93a0" wrap width={620}>
        Tab / Shift+Tab 前后遍历 · Enter/Space 激活 · ←→↑↓ 在组合控件里走 ·
        下拉/日历/色板 Enter 展开、Esc 收起 · 焦点在输入框时 Enter 提交表单
      </text>

      {/* ---- ④ 模态弹层: 打开后 Tab 在弹层内循环, Esc 关闭 ---- */}
      <dialog open={() => dialogOpen()} onClose={() => setDialogOpen(false)}>
        <column gap={10} padding={14}>
          <text font={14}>确认提交?</text>
          <text font={11} color="#8a93a0" wrap width={240}>
            弹层里按 Tab 只在弹层内循环; Esc 关闭后焦点自动校正。
          </text>
          <row gap={8}>
            <button aria-label="确认" onClick={() => { setDialogOpen(false); setSubmitted("已确认 —— " + submitted()); }}>确认</button>
            <button aria-label="取消" onClick={() => setDialogOpen(false)}>取消</button>
          </row>
        </column>
      </dialog>
    </column>
  </window>
);
