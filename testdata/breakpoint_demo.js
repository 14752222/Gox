// M4 演示: 自适应断点布局 —— 同一份代码在四个尺寸下自动换形态。
// 运行: ./gox testdata/breakpoint_demo.js
//
// 现象:
//   1. 顶部实时显示当前命中的断点档 (sm/md/lg/xl) 与完整的阈值表。
//   2. 点下面四个按钮 (或直接拖动窗口边缘) 改变宽度, 布局自动切换:
//        sm  (<600)   单栏, 无侧栏
//        md  (>=600)  侧栏 + 单列主区
//        lg  (>=840)  侧栏 + 两列主区
//        xl  (>=1200) 侧栏 + 两列主区 + 右侧信息栏
//   3. 换档只重新求值布局, 不重置任何状态 —— 断点就是普通响应式读数。
//
// 要点 (完整文档见 docs/multi-window.md §2):
//   - 断点是**命名阈值**, 默认表 sm:0 / md:600 / lg:840 / xl:1200 (dp), 与尺寸类
//     (widthClass 的 600/840 分界) 复用同一组数字。
//   - 断点按**窗口宽度**算, 不是屏幕宽度: 多窗口/分屏下每个窗口各算各的。
//   - useBreakpoint() 返回的是**取值函数 + 订阅** (信号语义); matchBreakpoint
//     自身不订阅, 所以它必须放在一个"先读 bp() 再取值"的函数里, 才能随宽度重算。
import { h, render } from "gx/gfx";
import { useBreakpoint, matchBreakpoint, breakpoints } from "gx/viewport";

const bp = useBreakpoint(); // () => "sm" | "md" | "lg" | "xl"

// 每一档一套布局参数。matchBreakpoint 在"给了值且是合法档名"的键里取
// 下界 <= 宽度 中最大的一档 —— 显式列全四档就不会落到 undefined。
const layout = () => {
  bp(); // ← 先无条件订阅; 少了这一句, 下面的取值不会跟着宽度重算
  return matchBreakpoint({
    sm: { cols: 1, side: false, info: false },
    md: { cols: 1, side: true, info: false },
    lg: { cols: 2, side: true, info: false },
    xl: { cols: 2, side: true, info: true },
  });
};

const Card = (props) => (
  <column background="#f4f6fb" padding={10} gap={4} radius={8}>
    <text font={13} color="#445">{props.title}</text>
    <text font={11} color="#889">自适应卡片</text>
  </column>
);

const thresholds = () => {
  const t = breakpoints();
  return ["sm", "md", "lg", "xl"]
    .filter((k) => t[k] !== undefined)
    .map((k) => `${k}@${t[k]}`)
    .join("   ");
};

let handle = null;
handle = render(
  <window title="breakpoint demo" width={520} height={440}>
    <column gap={10} padding={14}>
      <text font={16}>{() => `breakpoint = ${bp()}`}</text>
      <text font={11} color="#667">{() => "thresholds (dp):  " + thresholds()}</text>

      <row gap={8}>
        <button onClick={() => handle.resize(480, 440)}>sm 480</button>
        <button onClick={() => handle.resize(700, 440)}>md 700</button>
        <button onClick={() => handle.resize(900, 440)}>lg 900</button>
        <button onClick={() => handle.resize(1300, 440)}>xl 1300</button>
      </row>

      {() =>
        layout().side ? (
          <row gap={12}>
            <column width={150} gap={6} background="#eef1f6" padding={10} radius={8}>
              <text font={13} color="#445">Sidebar</text>
              <text font={12} color="#889">md 起显示</text>
            </column>

            <column gap={8} flexGrow={1}>
              <text font={13}>{() => `主区 ${layout().cols} 列`}</text>
              <row gap={8}>
                {() =>
                  [1, 2]
                    .slice(0, layout().cols)
                    .map((i) => <Card title={`Card ${i}`} />)
                }
              </row>
            </column>
          </row>
        ) : (
          <column gap={8}>
            <text font={13}>单栏 (sm)</text>
            <Card title="Card 1" />
          </column>
        )
      }

      {() =>
        layout().info ? (
          <column background="#fff6e5" padding={10} radius={8}>
            <text font={12} color="#7a5">xl 起显示的信息栏</text>
          </column>
        ) : null
      }
    </column>
  </window>
);

globalThis.bpWindow = handle;
void 0;
