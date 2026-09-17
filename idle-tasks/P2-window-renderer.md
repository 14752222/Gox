# 闲时任务 P2：Gox GUI —— Windows 窗口 + 软件渲染器 + 元素树

## 项目背景

- 仓库：当前工作区（Gox 项目根目录），纯 Go 实现的 JavaScript 引擎（Gox）。
- 总路线：SolidJS 式语法（JSX 降级为 `h()`）+ signals 细粒度响应式 + 自研渲染器，最终打包桌面程序。
- P1（本任务的前置）：`gx/solid` 模块（createSignal/createEffect/createMemo，Go 实现）、
  lexer/parser 的 JSX 支持（降级为 `h()` 调用）、`test/testdata/jsx_demo.js`。

## 前置检查（不满足则停止并写报告）

1. `go test ./...` 全绿；`test/testdata/jsx_demo.js` 能跑出节点树。
2. 确认 JSX 与 `gx/solid` 已存在（parser 测试里有 JSX 用例即算）。

## 任务范围

### 1. 新包 `gfx/`（仓库根目录，模块内路径 `github.com/14752222/Gox/gfx`）

允许的依赖：**仅 Go 标准库 + `golang.org/x/sys/windows`**。Gox 核心包
（lexer/parser/compiler/vm/stdlib 等）不得引入任何新依赖，也不得改动（唯一例外见第 4 点）。

平台抽象（为 P4 的 X11/macOS 后端预留）：定义接口

```go
type Surface interface { Show(img *image.RGBA); Events() <-chan Event }
type WindowFactory interface { Create(cfg WindowConfig) (Surface, error) }
```

Windows 实现放 `gfx/win32/`。

### 2. Win32 窗口（纯 syscall，不用 cgo）

- `RegisterClassEx` + `CreateWindowEx` 建窗口；`SetProcessDpiAwarenessContext` 开 DPI 感知。
- 帧缓冲：`CreateDIBSection`（32 位 BGRA），Go 侧把它当作 `image.RGBA` 直接写像素，`WM_PAINT` 时 `BitBlt` 上屏。
- 消息循环：`GetMessage`/`PeekMessage` + `DispatchMessage`；鼠标（`WM_LBUTTONDOWN`/`UP`）、
  关闭（`WM_CLOSE`/`WM_DESTROY`）、尺寸（`WM_SIZE`）事件。
- **WndProc 里绝不直接执行 JS**：一律投递到 PostTask 队列（见第 4 点）。

### 3. 元素树与渲染

- 真正的 `h(tag, props, ...children)`：构建 `GuiNode{tag, props, children, parent, box, dirty}`。
- 响应式接线：函数值的 props 和函数子节点用 `createEffect` 包一层
  （gfx 是 Go 包，直接调 `gx/solid` 的 Go 层 API），effect 内求值并写回节点属性、标脏，触发一帧重绘。
  文本/子节点替换同理。
- 软件光栅化（纯 Go，写到 image.RGBA）：实心矩形、1px 边框、背景色；
  颜色支持 `#rrggbb` 和少量命名色。**v1 不画文字**（P3 做字体），文本节点无 background 时跳过绘制。
- 最小布局：`column`/`row` 支持 `gap`、`padding`，节点支持 `width/height/background`。
  完整 flex 子集留给 P3。
- 命中测试：按布局框从顶到底找 onClick 目标。

### 4. 事件循环整合（对 vm 的唯一允许改动）

给 `vm/vm.go` 新增一个导出方法（不改既有行为）：

```go
// RunTimersWithPump 运行事件循环；空闲时调用 pump(maxWait) 等待外部事件，
// pump 返回 false 表示退出循环。GUI 模式用消息泵当 pump。
func (vm *VM) RunTimersWithPump(pump func(maxWait time.Duration) bool) error
```

实现上复用 `RunTimersUntil` 的主体，把"sleep 等待"替换为 pump 回调。gfx 侧的 pump：
`MsgWaitForMultipleObjects` 限时等待 + `PeekMessage` 排空 + drain PostTask + 处理脏区重绘。

**线程规则**：GUI 模式入口先 `runtime.LockOSThread()`，脚本执行、消息泵、VM 回调全部在这一个
线程串行；PostTask 队列是唯一跨 goroutine 入口（内部可用 mutex）。

### 5. `gx/gfx` 模块与演示

JS 侧 API：`import { h, window, render } from "gx/gfx"`；
`render(vnode, window({title, width, height}))` 挂载并进入事件循环。

新建 `test/testdata/gui_demo.js`（窗口演示，纯色块不依赖文字）：

```js
import { createSignal } from "gx/solid";
import { h, window, render } from "gx/gfx";
const [count, setCount] = createSignal(0);
render(
  <column gap={10} padding={16}>
    <rect width={() => count() * 20} height={24} background="#c0392b"/>
    <rect width={200} height={32} background="#27ae60" onClick={() => setCount(c => c + 1)}/>
  </column>,
  window({ title: "Gox GUI", width: 400, height: 300 })
);
```

`go run . test/testdata/gui_demo.js` 弹出窗口；点绿色块，红色块变宽。这是核心验收。

## 验收标准

1. `go build ./...`、`go test ./...` 全绿。
2. 光栅化/布局/命中测试有单元测试（渲染到 image.RGBA 断言像素，不需要真窗口）。
3. gui_demo 手动验证通过（在报告中描述现象）。
4. vm 的改动只有 `RunTimersWithPump` 及测试。

## 安全边界

不执行任何 git 操作；不回退/格式化无关文件（工作区有他人未提交改动）；
不引入白名单外的依赖，不用 cgo，不联网装东西；卡住超过 3 次尝试写报告停止。

## 交付物

仓库根目录写 `IDLE_TASK_REPORT_P2.md`：文件清单与意图、Surface 接口设计说明、
事件循环时序说明、测试结果原文、已知限制（如暂无文字）。
