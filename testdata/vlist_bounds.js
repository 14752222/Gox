// 取证脚本: 虚拟化长列表的**首帧建树**必须也是有界的。
// 被 gfx/vlist_test.go 的 TestVlistFirstBuildIsBounded 驱动。
//
// 为什么不复用 vlist_demo.js: 那个脚本要展示"十万行也很快", 数据量必须真到
// 十万才像样; 而这条测试要断言的是**渲染调用次数**, 两万行足够说明问题又能让
// 测试跑得快。两者的列表形状逐字一致 (scroll vlist → view each → row)。
//
// 导出 `__stats.rowCalls`: 渲染函数被调用的次数。这是唯一能证明"首帧没有把 N 行
// 全建一遍"的量 —— 树上最终的行数看不出来 (多余的会在同一轮里被销毁)。
import { h, render } from "gx/gfx";

const N = 20000;
const ROW_H = 28;

const rows = [];
for (let i = 0; i < N; i++) rows.push({ id: i, title: "第 " + i + " 行 · 首帧取证" });

// 计数放在普通对象上, 不用 `globalThis.x++` —— 后者在本机算出 NaN。
const stats = { rowCalls: 0 };
globalThis.__stats = stats;

render(
  h("column", { padding: 8 },
    h("scroll", {
      vlist: true,
      itemHeight: ROW_H,
      width: 420,
      height: 300,
    },
      h("view", { each: rows, key: "id" },
        (r, i) => {
          stats.rowCalls++;
          return h("row", { height: ROW_H, padding: 6 },
            h("text", { font: 12 }, i + " · " + r.title)
          );
        }
      )
    )
  ),
  { title: "vlist bounds", width: 470, height: 400 }
);
