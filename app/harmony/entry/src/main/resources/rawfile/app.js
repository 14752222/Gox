// Gox on HarmonyOS —— HF1/HF2 验收脚本。
//
// 约束: 这是 HAP 里的 rawfile 资产, 引擎拿到的只有**字符串** (没有文件系统) ⇒
// 只能 import 内置模块 (gx/xxx), 不能 import 相对路径的 .js 文件。
//
// 两块验收:
//   HF1 (通道) : 触摸 → 事件泵 → 界面响应; 安全区 inset 生效; 帧缓冲上屏。
//   HF2 (折叠) : 折痕/姿态上报 → gx/viewport 的**订阅式**读数跟着变。
//
// 脚本侧用 pixelRatio 做 dp → px 换算; 没写 font 的控件物理大小已由内核按
// Scale 缩放 (与 Android 侧同一套)。
//
// 响应式纪律: 凡是要跟着变的东西 (prop / 文本子节点) 都必须传**函数**。
// 写 x() 只是取一次快照, 之后静默不更新 —— 本工程最常见的坑, 且不会有警告。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";
import { useDeviceInfo } from "gx/device";
import { useInsets, useLayoutMode, useReservedRegions, hasFold } from "gx/viewport";
import { platform } from "gx/screen";

const dev = useDeviceInfo();
const k = dev.pixelRatio > 0 ? dev.pixelRatio : 1;
const px = (v) => Math.round(v * k);

// 三个订阅式读数: 每次调用都重新取 (读一次 = 订阅一次), 折一下就会醒。
// 注意 useReservedRegions 的形状与别的不一样 —— 它是"两段式": 外层调用先绑定
// 窗口并返回 getter, **内层**调用才既订阅又取值。别少写一层括号。
const ins = useInsets;
const lm = useLayoutMode();
const regions = useReservedRegions();

const [count, setCount] = createSignal(0);
const [name, setName] = createSignal("");

// 折痕摘要。
//
// ⚠️ 这里必须**先无条件取一次 regions()**, 短路会连带吃掉订阅: 两者之中只有
// regions() 带版本号信号, 而 hasFold() / posture() 是纯读数。写成
// `hasFold() ? "..." + regions()... : "无折痕"` 时首帧走的是 false 分支,
// regions() 根本没被调用 ⇒ 这个 effect 一个依赖都没有, 之后折起来**永远不会
// 重跑** —— 界面静态不动, 而且没有任何警告。(HF2 落地时实测踩到, 由
// gfx/mobile/harmony_asset_test.go 钉住。)
const foldSummary = () => {
  const r = regions();
  if (!hasFold()) {
    return "未检测到折痕 (直板机, 或宿主未上报)";
  }
  return "折痕 " + r.division.length + " 条 ｜ 保留区共 " + r.all.length + " 条";
};

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
      <text font={px(20)}>Gox on HarmonyOS</text>
      <text font={px(12)}>{() => "后端: " + platform()}</text>

      <text font={px(14)}>{() => "HF1 触摸链路: 计数 " + count()}</text>
      <button onClick={() => setCount((c) => c + 1)}>点我加一</button>

      <text font={px(14)}>HF2 折叠上报:</text>
      <text font={px(12)}>{() => "布局建议: " + lm().suggested + " ｜ 宽度档: " + lm().widthClass}</text>
      <text font={px(12)}>{() => "姿态: " + lm().posture}</text>
      <text font={px(12)}>{foldSummary}</text>

      <text font={px(14)}>点输入框弹软键盘, 试试中文输入:</text>
      <input
        height={px(36)}
        placeholder="点我输入"
        value={() => name()}
        onInput={(e) => setName(e.value)}
      />
      <text font={px(12)}>{() => "输入内容: " + name()}</text>
    </column>
  </window>
);
