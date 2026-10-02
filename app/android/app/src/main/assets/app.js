// Gox on Android —— 验收脚本: 点一下按钮计数加一, 底部随软键盘让位。
//
// 约束: 这是 APK 里的 asset, 引擎拿到的只有**字符串** (没有文件系统) ⇒ 只能 import
// 内置模块 (gx/xxx), 不能 import 相对路径的 .js 文件。
//
// 脚本侧用 pixelRatio 做 dp → px 换算; 默认字号随 Scale 的缩放已在内核做掉
// (没写 font 的控件物理大小自动正常)。
//
// 安全区: 宿主 (MainActivity) 把 WindowInsets 上报给 gx/viewport, 这里用
// useInsets() 响应式读, padding 让开状态栏/刘海/手势条。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";
import { useDeviceInfo } from "gx/device";
import { useInsets, useKeyboardHeight } from "gx/viewport";

const k = useDeviceInfo().pixelRatio > 0 ? useDeviceInfo().pixelRatio : 1;
const px = (v) => Math.round(v * k);

const [count, setCount] = createSignal(0);
const [name, setName] = createSignal("");
const ins = useInsets;
// 软键盘高度是**独立通道** (宿主 nativeSetKeyboard → gx/viewport): 它不随
// insets 走 (键盘弹起时安全区通常没变), 所以底部让位要把两者都算上。
// 注意这里必须**无条件调用** useKeyboardHeight() —— 它是订阅型读数, 写进
// 三元条件里短路掉就等于这个 effect 一个依赖都没有, 之后永不重跑 (静默失效)。
const kb = useKeyboardHeight;

render(
  <window title="Gox">
    <column
      gap={px(12)}
      padding={px(20)}
      paddingTop={() => ins().top + px(20)}
      paddingBottom={() => Math.max(ins().bottom, kb(), px(20))}
      paddingLeft={() => Math.max(ins().left, px(20))}
      paddingRight={() => Math.max(ins().right, px(20))}
    >
      <text font={px(20)}>Gox on Android</text>
      <text font={px(14)}>{() => "触摸链路已通: 计数 " + count()}</text>
      <button onClick={() => setCount((c) => c + 1)}>点我加一</button>
      <text font={px(14)}>点输入框弹软键盘, 试试中文输入:</text>
      <input
        height={px(36)}
        placeholder="点我输入"
        value={() => name()}
        onInput={(e) => setName(e.value)}
      />
      <text font={px(14)}>{() => "输入内容: " + name()}</text>
      <text font={px(12)}>{() => "键盘高(px): " + kb()}</text>
      <text font={px(10)}>{() => "insets: t=" + ins().top + " b=" + ins().bottom + " l=" + ins().left + " r=" + ins().right}</text>
    </column>
  </window>
);
