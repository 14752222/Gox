// Gox on iOS —— M1 验收脚本: 点一下按钮, 计数加一。
//
// 约束: 这是 App bundle 里的资源, 引擎拿到的只有**字符串** (没有相对路径文件)
// ⇒ 只能 import 内置模块 (gx/xxx), 不能 import 相对路径的 .js 文件。
//
// 移动端内核 (gfx/mobile) v1 只按**物理像素**布局, density 只上报不换算
// —— 所以脚本侧用 pixelRatio 自己做 dp → px 换算; 默认字号随 Scale 的
// 缩放已在内核做掉 (没写 font 的控件物理大小自动正常)。
//
// 安全区: 宿主 (GoxViewController) 把 safeAreaInsets 上报给 gx/viewport,
// 这里用 useInsets() 响应式读, padding 让开状态栏/刘海/Home 指示条。
// useInsets() 每次调用都订阅 viewport 版本号, insets 变化 (旋转/分屏) 时
// 响应式 prop 自动重算并标脏重绘。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";
import { useDeviceInfo } from "gx/device";
import { useInsets } from "gx/viewport";

const k = useDeviceInfo().pixelRatio > 0 ? useDeviceInfo().pixelRatio : 1;
const px = (v) => Math.round(v * k);

const [count, setCount] = createSignal(0);
const [name, setName] = createSignal("");
const ins = useInsets;

render(
  <window title="Gox">
    <column
      gap={px(12)}
      padding={px(20)}
      paddingTop={() => ins().top + px(20)}
      paddingBottom={() => Math.max(ins().bottom, px(20))}
      paddingLeft={() => Math.max(ins().left, px(20))}
      paddingRight={() => Math.max(ins().right, px(20))}
    >
      <text font={px(20)}>Gox on iOS</text>
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
    </column>
  </window>
);
