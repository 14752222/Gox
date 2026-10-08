// 演示: <scroll> 的滚动位置**写入口** (看板 r846P0) —— 受控 prop scrollTop / scrollLeft。
// 运行: ./gox testdata/scroll_to_demo.js
//
// 现象:
//   - 点「追加一行」: 列表变长, 因为 scrollTop 恒为 "bottom" (粘性别名), 视口
//     自动跟着贴到最新一行 —— 这正是日志查看器 / 聊天消息流要的"跟随最新";
//   - 期间用滚轮往上翻: 只要没追加新行, 视口不会被抢回去 (下沉权交给用户);
//   - 点「回到顶部」: scrollTop 换成 "top", 跳到第 0 行;
//   - 点「跳到第 6 行」: 写的是绝对像素 6*ROW_H, 一次跳到位 (越界会按内容
//     总高钳位, 所以写 99999 也等于"跳到底部")。
//
// 反向通道: 用户滚动会派发 onScroll({offsetX, offsetY}), 脚本据此能判断"用户
// 是不是还在底部", 从而决定要不要继续跟随 —— 这两个方向凑起来才是完整的
// "tail -f" 交互。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";

const ROW_H = 28;

const [lines, setLines] = createSignal(["line 0", "line 1", "line 2"]);
const [cmd, setCmd] = createSignal("bottom");
const [follow, setFollow] = createSignal(true);
const [pos, setPos] = createSignal(0);

function appendRow() {
  const n = lines().length;
  setLines(lines().concat(["line " + n]));
}
function jumpTop() {
  setFollow(false);
  setCmd("top");
}
function jumpRow(row) {
  setFollow(false);
  setCmd(row * ROW_H);
}
function jumpBottom() {
  setFollow(true);
  setCmd("bottom");
}

render(
  <window title="scrollTop demo" width={400} height={320}>
    <column gap={8} padding={12}>
      <text font={15}>滚动位置写入口: scrollTop / scrollLeft</text>

      <row gap={6}>
        <button onClick={appendRow}>追加一行</button>
        <button onClick={jumpTop}>回到顶部</button>
        <button onClick={() => jumpRow(6)}>跳到第 6 行</button>
        <button onClick={jumpBottom}>跟随最新</button>
      </row>

      <scroll
        width={300}
        height={140}
        border="#cccccc"
        background="#ffffff"
        scrollTop={() => (follow() ? "bottom" : cmd())}
        onScroll={(e) => setPos(e.offsetY)}
      >
        {() =>
          lines().map((t) => (
            <rect height={ROW_H} background="#f6f8fa">
              <text font={12} color="#333333">
                {t}
              </text>
            </rect>
          ))
        }
      </scroll>

      <text font={11} color="#5a6a7a">{() => "onScroll offsetY = " + pos()}</text>
    </column>
  </window>
);
