# JSON 工具箱

Gox 实用应用 **#1**（一期）：粘贴 JSON → 格式化 / 压缩 / 校验 / 路径查询。左右双栏，等宽字体。

## 运行

```bash
# 仓库内直接跑（用当前源码构建的二进制）
go build -o F:/tmp/gox-current.exe ./cmd/gox
F:/tmp/gox-current.exe apps/json-toolbox/src/main.js

# 或走 npm
cd apps/json-toolbox && npm install && npm run dev
```

## 打包成单文件

```bash
go run ./packager apps/json-toolbox/src/main.js --gui --windowed -o json-toolbox.exe
```

## 功能

| 操作 | 入口 | 快捷键 |
|---|---|---|
| 格式化（缩进 2/4/Tab 可选） | 工具栏 / 菜单「编辑」 | `Ctrl+Enter` |
| 压缩成一行 | 工具栏 / 菜单「编辑」 | — |
| 仅校验 | 工具栏 / 菜单「编辑」 | — |
| 打开 JSON 文件 | 菜单「文件」 | `Ctrl+O` |
| 另存结果 | 菜单「文件」 | `Ctrl+S` |
| 复制结果 | 工具栏 / 菜单「结果」 | `Ctrl+Shift+C` |
| 结果回填输入 | 工具栏 / 菜单「结果」 | — |
| 路径查询 `a.b[0].c` / `$.a["b"]` | 工具栏第二行 | 输入框内回车 |

**错误定位**：校验失败时状态栏给出**第几行第几列**与原因（尾逗号 / 键没引号 / 单引号 / 缺冒号 / 括号未闭合等）。

## 压到的框架能力

- `textarea` 受控编辑（软换行 / 选区 / 剪贴板）+ `fontFamily="monospace"`
- `menubar` 自绘菜单 + `shortcut` 快捷键（事件泵层匹配，菜单不必展开）
- `select` 受控下拉、`input` + `model` 双向绑定
- `gx/dialog` 原生打开/保存文件（async）、`gx/gfx` 剪贴板（同步）
- signal 驱动的跨组件状态（模块作用域 store）、`show` 指令、响应式 prop/子节点

## 已知限制与暴露的缺陷（写这个应用撞出来的）

1. **`JSON.parse` 的错误不带行列号**（走 Go encoding/json，`Object.keys(e)` 还是空的）——
   所以应用里自研了一个扫描器 `src/lib/json.js` 的 `locateError` 给出行列。
   合法性以原生 `JSON.parse` 为准，扫描器只负责"哪里错"。
2. **闭包写外层变量 / 模块级解构 const 的 setter** 有 VM 级缺陷 —— 看板单 `rLGyHa`。
   本应用的规避写法都标了注释（`locateError` 用对象承载状态、store 用索引取值 signal），
   **改代码时别把这两处改回常规写法**，会直接死循环 / ReferenceError。
