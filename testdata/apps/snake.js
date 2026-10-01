// 示例应用 3: 贪吃蛇 (T15 交付之三)。
// 运行: ./gox testdata/apps/snake.js
//
// 展示的能力:
//   1. canvas 逐帧自绘: 网格 / 蛇身 / 食物全部 fillRect + fillCircle;
//   2. 键盘输入: 根节点 onKeyDown (方向键 ArrowUp/Down/Left/Right),
//      事件沿祖先链冒泡到根 —— 游戏不需要"焦点"概念;
//   3. 游戏循环: setInterval 固定步长推进 (140ms/步), 死亡自动重开;
//   4. 响应式重绘: onDraw 里读 frame 信号, 每步 setFrame 触发重画;
//   5. 分数与最高分: gx/storage 持久化最高分 (重启还在)。
//
// 手动玩法: 方向键转向; 撞墙/咬到自己就重开。刻意不做暂停/加速 ——
// 示例应用以"能读懂"为先。
import { h, render, createSignal } from "gox";
import { setStorage, getStorage } from "gx/storage";

// ===== 常量 =====

const COLS = 16, ROWS = 12, CELL = 16;
const W = COLS * CELL, H = ROWS * CELL;

// ===== 状态 (游戏数据用普通对象, 只有"要画了"走信号) =====

let snake, dir, food, dead;
const [frame, setFrame] = createSignal(0);
const [score, setScore] = createSignal(0);
const [hi, setHi] = createSignal(Number(getStorage("gox-snake.hi")) || 0);

const spawnFood = () => {
  for (;;) {
    const f = { x: Math.floor(Math.random() * COLS), y: Math.floor(Math.random() * ROWS) };
    if (!snake.some((s) => s.x === f.x && s.y === f.y)) return f;
  }
};

const reset = () => {
  snake = [{ x: 8, y: 6 }, { x: 7, y: 6 }, { x: 6, y: 6 }];
  dir = { x: 1, y: 0 };
  dead = false;
  setScore(0);
  food = spawnFood();
};

reset();

// ===== 游戏逻辑 =====

const step = () => {
  if (dead) return;
  const head = { x: snake[0].x + dir.x, y: snake[0].y + dir.y };

  // 撞墙 / 咬自己 (尾巴尖会让位, 所以只咬到"除尾以外"的身体)
  const hitWall = head.x < 0 || head.y < 0 || head.x >= COLS || head.y >= ROWS;
  const body = snake.slice(0, snake.length - 1);
  const hitSelf = body.some((s) => s.x === head.x && s.y === head.y);
  if (hitWall || hitSelf) {
    dead = true;
    if (score() > hi()) {
      setHi(score());
      setStorage("gox-snake.hi", score());
    }
    setFrame(frame() + 1);
    return;
  }

  snake.unshift(head);
  if (head.x === food.x && head.y === food.y) {
    setScore(score() + 1);
    food = spawnFood();
  } else {
    snake.pop();
  }
  setFrame(frame() + 1);
};

const onKey = (e) => {
  const d =
    e.key === "ArrowUp" ? { x: 0, y: -1 } :
    e.key === "ArrowDown" ? { x: 0, y: 1 } :
    e.key === "ArrowLeft" ? { x: -1, y: 0 } :
    e.key === "ArrowRight" ? { x: 1, y: 0 } : null;
  // 不许 180° 掉头 (长度 1 时随意)
  if (d && !(d.x === -dir.x && d.y === -dir.y) && !(snake.length === 1)) dir = d;
};

setInterval(step, 140);

// ===== 界面 =====

const drawCell = (ctx, c, color) => {
  ctx.fillRect(c.x * CELL + 1, c.y * CELL + 1, CELL - 2, CELL - 2, color);
};

render(
  <window title="贪吃蛇 — Gox 示例应用" width={W + 32} height={H + 96}>
    <column gap={8} padding={16} onKeyDown={onKey}>
      <canvas
        width={W} height={H} background="#1b2027"
        onDraw={(ctx) => {
          frame(); // 订阅: 每 step 重画一次
          ctx.fillRect(0, 0, W, H, "#1b2027");
          // 网格点 (每格一个 1px 点, 让棋盘可感知)
          for (let gx = 0; gx < COLS; gx++) {
            for (let gy = 0; gy < ROWS; gy++) {
              ctx.fillRect(gx * CELL, gy * CELL, 1, 1, "#2a323c");
            }
          }
          drawCell(ctx, food, "#d84a3a");
          for (let i = snake.length - 1; i >= 0; i--) {
            drawCell(ctx, snake[i], i === 0 ? "#7fb069" : "#4a7a4f");
          }
          if (dead) {
            ctx.drawText("GAME OVER - press any arrow", 24, H / 2 - 8, 14, "#ffffff");
          }
        }}
      />
      <row gap={10}>
        <text font={14}>{() => "得分 " + score()}</text>
        <text font={14} color="#68707c">{() => "最高 " + hi()}</text>
        <text font={12} color="#98a0aa">方向键控制</text>
      </row>
    </column>
  </window>
);
