# docs/promo — 对外传播物料

本目录放对外发布用的稿件与投放说明。**成稿已完成，剩下的工作只有"复制粘贴 + 点发布"** ——
唯一的人工阻塞是平台账号（掘金 / 知乎需要人登录发帖），稿子本身不需要再改。

## 物料清单

| 文件 | 用途 | 状态 |
|---|---|---|
| [`gox-tech-article.md`](gox-tech-article.md) | 技术成稿（约 4.8k 中文字，掘金 / 知乎同一份） | ✅ 可发布 |

`gox-tech-article.md` 的三个钩子与数据来源：

1. **零 cgo 纯 Go 字节码 VM** —— lexer → parser → AST → bytecode → VM 全自研；
   基准数据取自 `bench-results/bench-20261009-131018.json`（Linux / Xeon）
   与 `bench-results/bench-20261002-024818.json`（Apple M2），对照系含
   `bench/goja`（纯解释器）与 node/V8（JIT）。
2. **test262 合规率下的取舍哲学** —— 用 `docs/test262-baseline.json`
   （language **78.48%**，18620/23726）与 `docs/test262-baseline-builtins.json`
   （built-ins **35.69%**，8503/23823）的**最新真实值**，失败面聚类取自
   `docs/test262-builtins-clusters.md`；核心论点是"v1 明确不做的清单比合规率数字更有价值"。
3. **自研 GUI 的分层纪律** —— `Surface` 五个方法的窄接口 + 可选能力走可选接口
   （`windowController` / `windowManager` / `cursorHost` / `windowMover` /
   `nativeVideoHost`…），换后端零改动、缺能力诚实降级；依据 `docs/gui-guide.md`、
   `docs/platform-config.md`、`docs/theme.md`。

> 注意：文中**不沿用**仓库里任何过时的合规率数字（例如早期文档里的 32.7%），
> 一律以 `docs/test262-*.json` 的当前值为准。

## 面向的平台

- **掘金**（juejin.cn）：Markdown 编辑器可直接粘贴全文；代码块语言标注已齐全
  （`bash` / `go` / `js` / `text` / `json`）。表格需确认渲染正常。
- **知乎**：支持 Markdown 导入，同一份文件即可；导入后检查标题层级与表格。

## 发布前需要人工做的事

### 1. 必改（否则不能发）

- [ ] **作者署名**：文末有一行
      `<!-- 发布前替换为你的署名与主页 -->`
      替换为真实署名（如「作者：xxx · GitHub @xxx」）。
- [ ] **标题微调（可选）**：现标题《我用 Go 写了个 JavaScript 引擎和原生 GUI 框架》。
      可加副标题，但**不要改成标题党** —— 文中所有数字都能在仓库里核对，
      标题夸大反而会被评论区当场拆穿。

### 2. 需要补的链接

仓库链接与官网链接**正文里已写好**（在「八、最后」一节）：

- GitHub：<https://github.com/14752222/Gox>
- 官网：<https://14752222.github.io/Gox/>
- npm：`npm i -g @goxjs/goxjs`

平台侧可能还要补：

- [ ] **掘金**：文章发布页选「后端 / Go」分类，标签建议 `Go` `JavaScript` `编译器` `GUI`；
      若平台要求封面图，见下节。
- [ ] **知乎**：发布时可选「添加话题」—— 建议 `Go 语言`、`JavaScript`、`编译器`；
      结尾可自行加一句「本文首发于 xxx」以避免搬运判定。

### 3. 建议补的截图（稿子里没有，需人工生成）

文中核心论点都是数字与代码，**不配图也能读**；但两处配图能显著提升点击率，
建议发布前补上（仓库里没有现成图，需要自己截）：

| 位置 | 建议的图 | 怎么生成 |
|---|---|---|
| 二、架构总览 | 一张真实的界面截图（Counter demo 出画面） | `./gox testdata/counter_demo.js` 后截屏 |
| 五、GUI 分层纪律 | 同一份 JSX 在 Windows / Linux / macOS 三个后端的窗口对比图 | 三平台各跑一次同一个 demo 拼图 |
| 三、性能一节（可选） | `bench-results/*.json` 里三个栈的柱状图 | 用 JSON 里的 `fib28_ms` / `startup_ms` 画 |

> 其他候选素材：仓库 `testdata/` 下有大量 demo 脚本（`feedback_demo.js`、
> `video_demo.js` 等），可直接跑出来截图；`gox-logo-concepts/` 是独立 submodule，
> 空目录的话先 `git submodule update --init`。

### 4. 发布前的最后核对

- [ ] 文中的数字有没有因为后续提交而过期 —— 重点核对 `docs/test262-*.json`
      的两个百分比（仓库有 CI 自动更新基线，改动很快）。
- [ ] 「七、v1 明确不做什么」里的条目是否已被兑付（对照 `docs/v1-roadmap.md` §七）。

## 剩下的阻塞

只有一条：**掘金 / 知乎账号需要人登录发帖**，无法自动化。稿件本身不涉及任何
需要人工决策的内容 —— 复制 `gox-tech-article.md` 全文 → 补署名 → 粘贴发布即可。
