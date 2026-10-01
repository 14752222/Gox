// 演示: 虚拟化长列表 <scroll vlist itemHeight={n}> —— 十万行只物化十几行。
// 运行: ./gox testdata/vlist_demo.js
//
// 关键写法: vlist 开关 + itemHeight 写在**外层 scroll** 上, 列表照旧用 each:
//
//   <scroll vlist itemHeight={28} height={300}>
//     <view each={rows} key="id">{(r, i) => <row height={28}>…</row>}</view>
//   </scroll>
//
// 内核只物化"可见区间 + buffer", 上下用撑高垫片补出总高 —— 于是滚动条长度、
// 滚动行程与全量渲染**逐像素一致**, 而内存与每帧成本只跟视口高 / itemHeight
// 成正比, 与行数无关。
//
// 现象:
//   - 十万行, 首帧与滚动都很快 (对比: 去掉 vlist 会卡到没法用);
//   - 滚动条滑块的比例与位置正确 (内容总高 = N * itemHeight);
//   - 快速拖动滚动条到最底, 末行内容正确 (不是空白, 也没有错位);
//   - 行内的序号是**全局下标** (第 50000 行显示 50000, 不是 0)。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";

const N = 100000;
const ROW_H = 28;

// 造十万行数据。真实场景里这是接口返回的分页/全量列表。
const rows = [];
for (let i = 0; i < N; i++) rows.push({ id: i, title: "第 " + i + " 行 · 虚拟化长列表" });

const [scrollTop, setScrollTop] = createSignal(0);
const [log, setLog] = createSignal("滚动它试试");

render(
  h("column", { gap: 10, padding: 16 },
    h("text", { font: 14 }, "虚拟化长列表: " + N + " 行"),
    h("text", { font: 11, color: "#8a93a0", wrap: true, width: 420 },
      "只物化可见的十几行; 上下垫片补出总高, 所以滚动条与全量渲染完全一致。"),

    h("scroll", {
      vlist: true,
      itemHeight: ROW_H,
      width: 420,
      height: 300,
      background: "#ffffff",
      border: "#dddddd",
      onScroll: (e) => { setScrollTop(e.offsetY); setLog("偏移 " + e.offsetY + "px"); },
    },
      h("view", { each: rows, key: "id" },
        (r, i) => h("row", {
          height: ROW_H,
          padding: 6,
          background: i % 2 === 0 ? "#fafbfc" : "#ffffff",
        },
          h("text", { font: 12, color: "#333333" }, i + " · " + r.title)
        )
      )
    ),

    h("text", { font: 11, color: "#4a5560" }, () => log())
  ),
  { title: "vlist demo", width: 470, height: 400 }
);
