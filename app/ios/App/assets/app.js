// Gox on iOS —— M1 验收脚本: 点一下按钮, 计数加一。
//
// 约束: 这是 App bundle 里的资源, 引擎拿到的只有**字符串** (没有相对路径文件)
// ⇒ 只能 import 内置模块 (gx/xxx), 不能 import 相对路径的 .js 文件。
//
// 移动端内核 (gfx/mobile) v1 只按**物理像素**布局, density 只上报不换算
// (M1 边界, 见 gfx/mobile.go 头注释) —— 所以脚本侧用 pixelRatio 自己做
// dp → px 换算, 高分屏上字号/间距才正常。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";
import { useDeviceInfo } from "gx/device";

const k = useDeviceInfo().pixelRatio > 0 ? useDeviceInfo().pixelRatio : 1;
const px = (v) => Math.round(v * k);

const [count, setCount] = createSignal(0);

render(
  <window title="Gox">
    <column gap={px(12)} padding={px(20)}>
      <text font={px(20)}>Gox on iOS</text>
      <text font={px(14)}>{() => "触摸链路已通: 计数 " + count()}</text>
      <button onClick={() => setCount((c) => c + 1)}>点我加一</button>
    </column>
  </window>
);
