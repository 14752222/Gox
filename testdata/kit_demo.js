// 样式体系方案 F 演示: 用户态设计套件 (2026-09-19 拍板落地)。
// 运行: go run . testdata/kit_demo.js
// 现象:
//   1. 同一组组件工厂 (Btn/Card/Field) 按 variant / size 展开成不同 props:
//      primary/ghost/normal × sm/md —— 变体就是工厂函数参数。
//   2. 右上角切换 light/dark 主题: 整个界面的颜色/间距跟随变化 —— 主题
//      就是"换一组令牌", 令牌读取走函数 prop, 天然响应式。
//   3. Btn 的悬停态用 onMouseMove/onMouseLeave + signal 近似 (内核 hoverable
//      白名单之外的自定义组件只能这么做; 边界见 docs/gui-patterns.md §4)。
//
// 要点 (docs/gui-patterns.md §4, 能力边界必须诚实):
//   - ✅ 能做: 令牌集中 / 变体尺寸 / 暗色主题 / 按状态改 background-color-
//     font-padding (这些 prop 都是响应式的)。
//   - ❌ 做不到 (内核把值写死): 焦点框颜色、滚动条颜色、圆角、阴影、
//     边框宽度 —— 套件在这些地方会"露出底", 属预期。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";

// ===== 令牌: 主题就是一组普通对象 =====

const light = {
  bg: "#f5f6f8", surface: "#ffffff", ink: "#1c2430", sub: "#68707c",
  accent: "#3355aa", accentHover: "#4a6bc4", ghostHover: "#e8ecf4",
  padX: 14, padY: 10, gap: 10, fontSm: 13, fontMd: 14,
};

const dark = {
  bg: "#1b2027", surface: "#242b33", ink: "#dfe6ee", sub: "#8b95a2",
  accent: "#6c8fd9", accentHover: "#85a4e8", ghostHover: "#2d3641",
  padX: 14, padY: 10, gap: 10, fontSm: 13, fontMd: 14,
};

const [themeName, setThemeName] = createSignal("light");
const t = () => (themeName() === "dark" ? dark : light);

// ===== 组件工厂: 变体/尺寸在工厂内部展开成今天的 props =====

// 注意: JSX 里 <Comp> 翻译成 Comp(props, ...children) —— children 是**变参**
// 不是 p.children; 无属性的 <Comp> 会让 props 是 null, 所以每个工厂都先兜底。
const Btn = (p, ...kids) => {
  p = p || {};
  // 悬停近似: onMouseMove 只会在命中链到本按钮时派发, onMouseLeave 收尾
  // (事件粒度是"移动"而非"进入/离开", 方案 F 的已知近似, 见文件头)
  const [hover, setHover] = createSignal(false);
  const variant = p.variant || "normal";
  const size = p.size || "md";
  const pad = size === "sm" ? 6 : 10;
  const font = size === "sm" ? () => t().fontSm : () => t().fontMd;
  return (
    <button
      padding={pad}
      font={font}
      color={() => (variant === "primary" ? "#ffffff" : t().ink)}
      background={() => {
        if (variant === "primary") return hover() ? t().accentHover : t().accent;
        return hover() ? t().ghostHover : t().surface;
      }}
      onMouseMove={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
      onClick={p.onClick}
      disabled={p.disabled}
    >{kids}</button>
  );
};

const Card = (p, ...kids) => (
  <column background={() => t().surface} padding={() => t().padX} gap={() => t().gap}>
    {kids}
  </column>
);

const Field = (p) => (
  <row gap={8}>
    <text font={() => t().fontSm} color={() => t().sub}>{p.label}</text>
    <input value={p.value} onInput={p.onInput} width={p.width || 160} />
  </row>
);

// ===== 界面 =====

const [name, setName] = createSignal("gox");
const [clicks, setClicks] = createSignal(0);

render(
  <window title="design kit (userland)" width={440} height={360}>
    <column gap={() => t().gap} padding={() => t().padX} background={() => t().bg}>
      <row gap={10}>
        <text font={17} color={() => t().ink}>Design kit demo</text>
        <Btn variant="primary" size="sm" onClick={() => setThemeName(themeName() === "dark" ? "light" : "dark")}>
          {() => themeName() === "dark" ? "light theme" : "dark theme"}
        </Btn>
      </row>

      <Card>
        <text font={() => t().fontMd} color={() => t().ink}>Variants &amp; sizes</text>
        <row gap={8}>
          <Btn variant="primary" onClick={() => setClicks(clicks() + 1)}>{() => `primary (${clicks()})`}</Btn>
          <Btn onClick={() => setClicks(clicks() + 1)}>normal</Btn>
          <Btn size="sm">small</Btn>
          <Btn disabled={true}>disabled</Btn>
        </row>
        <Field label="name" value={name} onInput={(e) => setName(e.value)} />
        <text font={() => t().fontSm} color={() => t().sub}>{() => `hello, ${name()}`}</text>
      </Card>

      <text font={() => t().fontSm} color={() => t().sub}>
        Tokens drive colors/spacing; hover is approximated with onMouseMove signals.
        Rounded corners / shadows / focus-ring color stay kernel-fixed (v1 limits).
      </text>
    </column>
  </window>
);
