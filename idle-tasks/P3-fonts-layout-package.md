# 闲时任务 P3：Gox GUI —— 文字渲染 + 布局 + 打包成 GUI exe

## 项目背景

- 仓库：当前工作区（Gox 项目根目录），纯 Go 的 JS 引擎 Gox。
  路线：SolidJS 式 JSX + signals + 自研渲染器。
- P1：`gx/solid`、JSX 降级 `h()`。P2（本任务前置）：`gfx/` 包 + `gfx/win32` 窗口 +
  软件光栅化 + `vm.RunTimersWithPump` 事件循环 + `test/testdata/gui_demo.js` 可弹窗交互。

## 前置检查（不满足则停止并写报告）

`go test ./...` 全绿；`go run . test/testdata/gui_demo.js` 能弹窗且点击有反应
（无法验证时以 P2 报告 + 代码为准并注明）。

## 任务范围

### 1. 文字渲染（`gfx/font.go`）

- 依赖白名单放宽到：标准库 + `golang.org/x/sys/windows` + `golang.org/x/image`（Go 官方维护）。
- 用 `x/image/font/sfnt` 解析系统字体：Windows 优先微软雅黑（`C:\Windows\Fonts\msyh.ttc`），
  回退 Segoe UI/simsun；字体缺失时报清晰错误。
- 按字体+字号+rune 做 LRU glyph 位图缓存；跑通中英文混排（不做复杂 shaping，
  CJK/拉丁按码位直排即可，在报告里写明此限制）。
- 文本节点测量：给 `text` 节点提供固有尺寸，供布局使用。

### 2. 布局子集（flex 风格）

在 P2 的 column/row 基础上补：`margin`、`alignItems`（start/center/end）、
`justifyContent`（start/center/end/between）、`flexGrow` 简版、文本自动宽度/换行
（单行优先，可截断）。写布局单元测试（给定树断言各节点 box）。

### 3. 脏矩形与帧率

- 节点级 damage：signal 更新只标脏受影响子树，合并脏矩形，局部重绘局部上屏。
- 基准：写一个 1000 节点树单信号变化的 bench，报告单帧耗时。

### 4. 键盘事件与 rAF

- `WM_CHAR`/`WM_KEYDOWN` 接入事件回流；JS 侧 `onKeyDown` 等 props 可用。
- 暴露 `requestAnimationFrame(cb)`（挂在事件循环的帧调度上，gfx 模式下驱动重绘用）。

### 5. jsbuild 支持 GUI 打包（改 `packager/main.go`）

- 加 `--gui` 标志：生成另一套 main.go 模板——`runtime.LockOSThread()` 后执行脚本，
  结尾用 `vm.RunTimersWithPump(gfx 的消息泵)` 替代 `RunTimers()`；生成的 go.mod 仍只需 replace 本仓库。
- 端到端验收：

```js
import { createSignal } from "gx/solid";
import { h, window, render } from "gx/gfx";
const [count, setCount] = createSignal(0);
render(
  <column gap={8} padding={16}>
    <text font={20}>{() => `count: ${count()}`}</text>
    <button onClick={() => setCount(c => c + 1)}>加一</button>
  </column>,
  window({ title: "Counter", width: 400, height: 300 })
);
```

`go run . test/testdata/counter_demo.js` 窗口中显示 count、点"加一"文字实时变；
`cd packager && go run . ..\test\testdata\counter_demo.js --gui --name counter -o counter.exe`
产出的 exe 双击可独立运行同样效果。

## 验收标准

1. `go build ./...`、`go test ./...` 全绿（含新增字体/布局/脏区测试）。
2. counter_demo 源码运行与打包 exe 运行均通过（报告中描述现象）。
3. 依赖白名单外零新增；Gox 核心包（lexer/parser/compiler/vm/stdlib）除 P2 已有改动外不再改。

## 安全边界

同前：无 git 操作、不动无关文件、不联网装东西（`golang.org/x` 依赖用 `go get` 拉取属于
唯一允许的网络操作）、卡住 3 次写报告停止。

## 交付物

仓库根目录写 `IDLE_TASK_REPORT_P3.md`：改动清单、字体方案与限制、布局覆盖面、
bench 数据、打包命令与验证结果。
