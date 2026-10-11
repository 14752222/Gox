# files —— 文件管理器（桌面）

双栏文件浏览器：左侧目录树（当前路径的面包屑 + 上级入口），右侧文件列表 +
文本文件预览。真实读写文件系统，不是 mock。

## 运行

```bash
go build -o /tmp/gox-current ./cmd/gox
/tmp/gox-current apps/files/src/main.js
```

## 压到的能力

- **`fs` 核心模块**：`readdirSync`（目录列表）/ `statSync`（大小、类型、
  mtime）/ `readFileSync`（文本预览），错误（权限不足、非目录）就地显示；
- **`each` + `key` 的 keyed 列表**：进入/返回目录时行集按路径复用；
- **受控 `scroll` 容器**：长目录滚动；
- **面包屑导航**：路径段可点，点击跳回任意上级；
- **signal 驱动视图**：当前路径 / 选中文件 / 错误条全部响应式。

## 结构

```
src/
  main.js        入口：render(<window>)
  app.js         根组件：面包屑 + 列表 + 预览 + 状态行
  store.js       模块作用域 signal：cwd / entries / selected / error
  theme.js       设计令牌
  lib/fsx.js     纯逻辑：列目录 / 排序（目录在前）/ 大小格式化 / 路径拼接
```

## 已知限制

- 预览只读文本（<= 64KB，超出截断提示）；二进制不猜编码；
- 不做写操作（新建/删除/重命名）—— 文件管理器的写路径需要确认对话框与
  回收站语义，超出本示例范围。
