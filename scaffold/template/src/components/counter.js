// 局部状态 + 受控控件。
//
// 组件就是普通函数：签名的参数是 props 对象，返回值是元素树。
// 大写 JSX 标签 <Counter/> 在 parser 层被降级成 Counter(...) 调用，没有运行时组件实例。
//
// 状态建在组件函数里 = 局部状态；建在 store.js 的模块作用域 = 共享状态。
import { h, createSignal } from "gox";
import { colors, space, font } from "../theme.js";

export function Counter() {
  const [count, setCount] = createSignal(0);
  const [step, setStep] = createSignal(1);

  return (
    <column gap={space.sm}>
      <text font={font.lg} color={colors.text}>{() => "count = " + count()}</text>
      <text font={font.sm} color={colors.muted}>{() => "step = " + step()}</text>

      <row gap={space.sm} alignItems="center">
        {/* setCount 也接受函数式更新：新值基于旧值算，不必自己读一遍再写 */}
        <button padding={4} onClick={() => setCount((c) => c - step())}>- step</button>
        <button padding={4} onClick={() => setCount((c) => c + step())}>+ step</button>
        {/* disabled 传函数才是响应式的：count 为 0 时按钮自动变灰且不响应点击 */}
        <button padding={4} disabled={() => count() === 0} onClick={() => setCount(0)}>reset</button>
      </row>

      {/* 受控滑块：显示只看 value、改动只派发 onInput（e.value 是 number）。
          不回写 value 的话滑块会弹回原位 —— 这是"受控"的定义，不是 bug。 */}
      <slider
        width={240}
        min={1}
        max={10}
        step={1}
        value={() => step()}
        onInput={(e) => setStep(e.value)}
      />

      {/* 展示型组件同样吃响应式 prop：函数一变化就标脏重画 */}
      <progress width={240} value={() => Math.min(Math.abs(count()) / 20, 1)} />
    </column>
  );
}
