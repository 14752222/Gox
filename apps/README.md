# apps/ —— Gox 实用应用集

这里是**真实可用的应用**，不是 API 示例。

| 目录 | 定位 | 与谁区分 |
|---|---|---|
| `scaffold/template/` | `gox create` 生成的新工程模板 | 起点，不是成品 |
| `testdata/*.js` | 单个 API / 组件的**最小演示**（一个文件一个主题） | 教 API 怎么用 |
| **`apps/<name>/`** | **完整应用**（多文件、有真实使用价值、能打包分发） | 能拿去用 |
| `docs/` | 已定稿的对外手册 | 文档，不放代码 |

## 目录结构约定

每个应用一个子目录，结构对齐 `gox create` 的产物（这样从应用倒推学脚手架、或把应用当模板都顺）：

```
apps/<name>/
  README.md            # 这个应用做什么 / 怎么跑 / 压到了哪些能力 / 已知限制
  package.json         # npm run dev 用；goxjs 走 npx
  gox.json             # 多平台打包配置（应用名 / appId / 权限）
  src/
    main.js            # 入口：render(<window>…)
    app.js             # 根组件
    store.js           # 跨组件共享状态（模块作用域 signal）
    theme.js           # 设计令牌：颜色 / 间距 / 字号
    lib/               # 纯逻辑，不 import gfx（能脱离 UI 单测）
    components/        # UI 组件
```

- **`lib/` 里的东西不许 import `gox`** —— 纯函数才好在探针脚本里单独跑（本仓库主力验证手段）。
- **设计令牌每个应用自带一份**，不共享：应用要能整个目录拷走独立运行，依赖 `../_shared` 会断。
  真正值得复用的（图表、Markdown 解析器）等第二个应用出现时再抽。

## 运行

```bash
# 仓库内直接跑（用当前源码构建的二进制，别用仓库根那个常年落后的 Gox.exe）
go build -o F:/tmp/gox-current.exe ./cmd/gox
F:/tmp/gox-current.exe apps/json-toolbox/src/main.js

# 或走 npm（需 npm i -g @goxjs/goxjs）
cd apps/json-toolbox && npm run dev
```

## 打包成单文件

```bash
go run ./packager apps/json-toolbox/src/main.js --gui -o json-toolbox.exe
```

## 写应用时必须守的几条

这几条**违反了不会报错**，只会静默失效 —— 所以放在这里当清单。

前四条已经机器化了，跑 `gox lint`（CI 里也会跑 `gox lint apps`）：

```bash
gox lint apps            # 扫整个 apps/
gox lint src/app.js      # 只扫一个文件
gox lint --skip=each-no-key    # 临时跳过某条规则
```

| 规矩 | lint 规则 |
|------|-----------|
| 子节点写成快照（`{sig()}`） | `snapshot-child` |
| 子节点区的 `//` 不是注释 | `slash-comment` |
| 列表用 `each` 时必须 `key` | `each-no-key` |
| `lib/` 不 import `gox` | `lib-imports-gox` |

第 5 条（新增应用后登记冒烟用例）涉及 `gfx/apps_smoke_test.go` 的用例表，
还没机器化 —— 改了那边记得同步。

1. **JSX 属性与子节点在调用当场求值一次**。需要响应式就必须传**函数**：`value={() => sig()}`，
   子节点写 `{() => sig()}` 而不是 `{sig()}`。**子节点写成快照是全静默的**（属性误用有警告，子节点没有）。
2. **JSX 子节点区的 `//` 不是注释** —— 会被当成一个文本子节点渲染出来且不报错。
   要写注释用 `{/* … */}` 或挪到 JSX 外。
3. **列表用 `each` 时必须 `key`**，且更新时**造新对象**（原地改字段引用没变，行不会重建）。
4. **`lib/` 保持纯逻辑**：不 import `gox`，不碰 signal，输入输出都是普通值。
5. 新增应用后，在 `gfx/apps_smoke_test.go` 的用例表里加一行（挂载 + 首帧 + 关窗的冒烟）。
