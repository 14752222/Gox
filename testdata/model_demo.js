// gx/model 演示: 受控组件的双向绑定 (model 指令)
// 运行: go run . testdata/model_demo.js
//
// 一条指令替掉手写的两个 prop:
//
//   <input value={() => draft()} onInput={(e) => setDraft(e.value)} />   // 旧
//   <input model={draft} />                                              // 新
//
// 读 (signal → 控件) 与写 (控件 → signal) 都由内核接好, 于是两个经典静默坑一起消失:
//   - value 漏了函数括号 ⇒ 快的只是第一帧的快照, 之后 signal 再变也不更新;
//   - 忘了写 onInput      ⇒ 编辑结果无处可去, 表现为"输入框打不进字"。
//
// For / Show / Switch / Match 的用法一个字都没变 —— 这条指令只作用于受控组件本身
// (它的每一次读写在界面上都看得见: 第 ③ 行 state 是全部绑定的投影)。
//
// 语义表 (每个标签一行, 没有例外):
//   input · textarea    model ⇄ value      onInput({value})   字符串
//   slider              model ⇄ value      onInput({value})   数字 (不转换)
//   select              model ⇄ value      onChange({value})  字符串
//   checkbox · switch   model ⇄ checked    onClick()          布尔 (写入取反)
//   radio               model ⇄ checked    onClick()          选中时把 value 属性写进 model
//   model 可传: signal (自带 setter) 或 [get, set] 二元组 (自定义来源)
import { h, render, createSignal, For, Show, Switch, Match } from "gox";

// ===== 状态 (界面全部派生自这几个 signal, 不存第二份) =====

const [raw, setRaw] = createSignal("hand-written"); // 对照用: 手写受控
const [sugar, setSugar] = createSignal("model"); // 对照用: model 指令
const [name, setName] = createSignal("Ada");
const [bio, setBio] = createSignal("");
const [volume, setVolume] = createSignal(40);
const [city, setCity] = createSignal("beijing");
const [agree, setAgree] = createSignal(false);
const [dark, setDark] = createSignal(false);
const [plan, setPlan] = createSignal("free");
const [profile, setProfile] = createSignal({ nick: "" }); // 自定义来源 (嵌套字段)
const [phase, setPhase] = createSignal("editing"); // Switch 的多状态

const cities = ["beijing", "shanghai", "shenzhen"];
const phaseIs = (p) => phase() === p;

// Line 是"标签 + 控件们"的通用外壳: 把每个标签的示例压成一行, 好在 620 高里全放下。
// 注意 JSX 的组件子节点是**变参** (`(p, ...kids)`), 不是 p.children。
const Line = (p, ...kids) => (
  <row gap={8} alignItems="center">
    <text font={12} width={122} color="#4a5560">{p.label}</text>
    {kids}
  </row>
);

// ===== 界面 =====

render(
  <window title="gx/model — two-way binding" width={700} height={620}>
    <column gap={9} padding={14}>
      <text font={17}>gx/model — 受控组件的一条指令</text>
      <text font={11} color="#8a93a0" wrap width={660}>
        model 一次接好读 (signal → 控件) 与写 (控件 → signal)。For / Show / Switch / Match 的用法不变。
      </text>

      {/* ---- ① 对照: 手写两个 prop vs 一条指令 ---- */}
      <column gap={6} background="#f5f6f8" padding={10}>
        <text font={12} color="#8a93a0">① 同一个输入框, 两种写法</text>
        {/* 这就是 model 内部展开出来的东西, 一行都省不掉 */}
        <Line label="手写 · 2 个 prop">
          <input width={168} value={() => raw()} onInput={(e) => setRaw(e.value)} />
          <text font={11} color="#8a93a0">{() => "raw = " + raw()}</text>
        </Line>
        <Line label="model · 1 个 prop">
          <input width={168} model={sugar} />
          <text font={11} color="#2e7d32">{() => "sugar = " + sugar()}</text>
        </Line>
      </column>

      {/* ---- ② 每个受控组件都是一个写法 ---- */}
      <column gap={6} background="#f5f6f8" padding={10}>
        <text font={12} color="#8a93a0">② 每个受控组件都是一条 model</text>

        <Line label="input">
          <input width={168} model={name} />
          <text font={11} color="#2e7d32">{() => "name = " + name()}</text>
        </Line>

        <Line label="textarea">
          <textarea width={168} rows={2} model={bio} />
          <text font={11} color="#2e7d32">{() => bio().length + " chars"}</text>
        </Line>

        <Line label="slider (数字)">
          <slider width={130} min={0} max={100} model={volume} />
          <text font={11} color="#2e7d32">{() => "volume = " + volume()}</text>
        </Line>

        <Line label="select">
          <select width={130} options={cities} model={city} />
          <text font={11} color="#2e7d32">{() => "city = " + city()}</text>
        </Line>

        <Line label="checkbox">
          <checkbox model={agree} />
          <text font={11} color="#2e7d32">{() => "agree = " + agree()}</text>
        </Line>

        <Line label="switch">
          <switch model={dark} />
          <text font={11} color="#2e7d32">{() => "dark = " + dark()}</text>
        </Line>

        {/* radio 的互斥不是"两边各写一遍取反", 而是共用一个 model 的必然结果 */}
        <Line label="radio ×2">
          <radio model={plan} value="free" />
          <text font={11} color="#4a5560">free</text>
          <radio model={plan} value="pro" />
          <text font={11} color="#4a5560">pro</text>
          <text font={11} color="#2e7d32">{() => "plan = " + plan()}</text>
        </Line>

        {/* 自定义来源: 手里没有 signal 时传 [get, set] 二元组 (形状与 createSignal 一致) */}
        <Line label="[get, set] 来源">
          <input width={168} model={[() => profile().nick, (v) => setProfile({ nick: v })]} />
          <text font={11} color="#2e7d32">{() => "profile.nick = " + profile().nick}</text>
        </Line>
      </column>

      {/* ---- ③ 全部绑定的投影 ---- */}
      <row gap={8} alignItems="center" background="#eef1f5" padding={8}>
        <text font={12} color="#4a5560" width={64}>③ state</text>
        <text font={11} color="#3a4450" wrap width={572}>
          {() => "name=" + name() + " · bio=" + bio().length + " 字符 · volume=" + volume() +
            " · city=" + city() + " · agree=" + agree() + " · dark=" + dark() + " · plan=" + plan()}
        </text>
      </row>

      {/* ---- ④ Switch: 表单状态 (用法与改动前完全一致) ---- */}
      <row gap={8} alignItems="center">
        <text font={13} width={64}>④ 状态</text>
        <button padding={4} disabled={() => phaseIs("editing")} onClick={() => setPhase("editing")}>editing</button>
        <button padding={4} disabled={() => phaseIs("saved")} onClick={() => setPhase("saved")}>save</button>
        <button padding={4} disabled={() => phaseIs("nope")} onClick={() => setPhase("nope")}>bad state</button>
      </row>
      <Switch
        fallback={
          <row gap={8} alignItems="center">
            <text font={12} color="#a0522d">没有任何 Match 为真 ⇒ fallback (Switch 的用法没变)</text>
          </row>
        }
      >
        <Match when={() => phaseIs("editing")}>
          <row gap={8} alignItems="center">
            <text font={12} color="#68707c">editing — 上面每个控件改一下, 第 ③ 行都会跟着动</text>
          </row>
        </Match>
        <Match when={() => phaseIs("saved")}>
          <row gap={8} alignItems="center">
            <text font={12} color="#2e7d32">saved — 值已经在 signal 里, 提交时直接读 name() / plan() / ...</text>
            <button padding={4} onClick={() => setPhase("editing")}>back</button>
          </row>
        </Match>
      </Switch>
    </column>
  </window>
);
