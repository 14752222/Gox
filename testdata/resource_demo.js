// 状态管理方案 B 演示: createResource (2026-09-19 拍板落地)。
// 运行: go run . testdata/resource_demo.js
// 现象:
//   1. 打开时显示 "loading..." (state = pending), 800ms 后变成列表
//      (state = ready) —— 三件套 (loading/error/data) 全在 res.state()/res.error()/data()。
//   2. 点 "refetch": 旧列表**保留显示** (state = refreshing, 不闪白屏),
//      取回来后更新。点 "fail once": 模拟一次失败 —— 列表保留 (data() 不抛
//      不丢, 返回上一次的值), 红字显示错误, 再点 refetch 恢复。
//   3. 连点两下 refetch: 前一次的响应会被 latest-wins 丢弃, 界面不会闪回旧值。
//
// 要点 (docs/gui-patterns.md §2):
//   - const [data, res] = createResource(fetcher) —— res 里是 refetch/state/error
//     (引擎不支持嵌套解构, state 不挂在 data 上, 这是与 Solid 的已文档化差异)。
//   - error 态 data() 不抛异常: 返回上一次的值, 错误只从 res.error() 读。
//   - fetcher 返回 Promise 走异步, 返回普通值则立即可用 (这里用 setTimeout
//     模拟网络延迟; 真实取数换成 http/f fs 读取即可)。
import { createSignal, createResource } from "gx/solid";
import { h, render } from "gx/gfx";

let failNext = false;
let seq = 0;

const fetchItems = () => new Promise((resolve, reject) => {
  const my = ++seq;
  setTimeout(() => {
    if (failNext) {
      failNext = false;
      reject(`fetch #${my} failed`);
      return;
    }
    resolve([`item ${my}-a`, `item ${my}-b`, `item ${my}-c`]);
  }, 800);
});

const [data, res] = createResource(fetchItems);

const stateFace = () =>
  res.state() === "error" ? "#b00020" : res.state() === "ready" ? "#2b7a4b" : "#8a6d00";

render(
  <window title="createResource demo" width={440} height={320}>
    <column gap={10} padding={16}>
      <row gap={10}>
        <text font={16}>state:</text>
        <text font={16} color={stateFace}>{() => res.state()}</text>
      </row>

      {() => {
        const st = res.state();
        if (st === "pending") return <text font={14}>loading...</text>;
        if (st === "error") {
          return (
            <column gap={6}>
              <text font={14} color="#b00020">{`error: ${res.error()}`}</text>
              <text font={13}>data() keeps the last good value below (never throws):</text>
            </column>
          );
        }
        return (
          <column gap={4}>
            {data().map((s) => <text font={14} key={s}>{`- ${s}`}</text>)}
          </column>
        );
      }}

      <row gap={10}>
        <button onClick={() => res.refetch()} disabled={() => res.state() === "pending"}>
          {() => (res.state() === "refreshing" ? "refreshing..." : "refetch")}
        </button>
        <button onClick={() => { failNext = true; res.refetch(); }}>fail once</button>
      </row>
      <text font={12} color="#889">double-click refetch fast: latest response wins, no flick-back.</text>
    </column>
  </window>
);
