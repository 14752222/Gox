// Gox on Android —— M1 验收脚本: 点一下按钮, 计数加一。
//
// 约束: 这是 APK 里的 asset, 引擎拿到的只有**字符串** (没有文件系统) ⇒ 只能 import
// 内置模块 (gx/xxx), 不能 import 相对路径的 .js 文件。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";

const [count, setCount] = createSignal(0);

render(
  <window title="Gox">
    <column gap={12} padding={20}>
      <text font={20}>Gox on Android</text>
      <text font={14}>{() => "触摸链路已通: 计数 " + count()}</text>
      <button onClick={() => setCount((c) => c + 1)}>点我加一</button>
    </column>
  </window>
);
