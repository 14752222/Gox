# 参与贡献 Gox

感谢你愿意给 Gox 提代码！这个项目**小而完整**：lexer → parser → compiler → VM → GUI → 移动端壳，
每一层都是从零写的 —— 也因此每一层都有可以独立上手的部分。本文档帮你最快找到入口。

## 找到适合你的任务

- 浏览带 [`good first issue`](https://github.com/14752222/Gox/labels/good%20first%20issue) 标签的 issue —— 适合首次贡献。
- 想做大的方向（组件库、移动端、性能），先读 [README 的路线图](README.md#路线图)（P1/P2 里程碑）再认领。
- 不确定某行为是 bug 还是约定？先去 [Discussions](https://github.com/14752222/Gox/discussions) 问一句，别白写一个 PR。

## 开发环境

| 需要 | 说明 |
|---|---|
| Go 1.26+ | 引擎与 GUI 全部是纯 Go（**桌面侧零 cgo**，无需任何 C 工具链） |
| Node | 只在发 npm 包 / 官网构建时需要，日常开发不需要 |

克隆（注意是 SSH 还是 HTTPS remote 均可）后先跑一遍本地闸门：

```bash
gofmt -l .             # 应无输出
go build ./...
go vet ./...
go test ./...
```

全绿再动手。CI（[ci.yml](.github/workflows/ci.yml)）在 PR 上会跑同样的检查，
外加一道「注册表一致性」闸门（见下文「四份同步」）。

## 提交规范

沿用仓库现有的 Conventional Commits 风格，scope 用受影响的模块或主题，中文描述：

```
feat(gfx): 新增 <slider> 的键盘调节支持
fix(vm): 修正 SET_INDEX 对非对象类型静默丢弃的问题
docs(gui-guide): 补充多窗口事件泵的轮询语义
ci(release): 打包闸改回 tar 校验，不再解析 npm 的输出
```

## 这个仓库最特殊的纪律：改一处、同步多处

Gox 有几处**必须手工同步的注册表**，漏改一律不报编译错误，而是静默失效。
这是本项目 bug 的主要来源，也是 CI 有专门一道闸门的原因。

### 1. 新增内置 GUI 组件 → 同步四处

| 位置 | 作用 |
|---|---|
| `gfx/node.go` 的 `knownTags` | 注册标签名，否则 `h()` 打印「未知标签」警告 |
| `gfx/layout.go` 的 `intrinsicSize` | 声明固有尺寸 |
| `gfx/layout.go` 的布局分派 | 声明子节点如何参与布局 |
| `gfx/raster.go` 的 `drawNode` | 实现绘制；被 `case` 截走的标签需自行补画 background / border |

### 2. 新增 gx/* 内置模块 → 同步三处

模块名进 `stdlib` 的 submodules 聚合表、`compiler.checkBuiltinImportNames` 的已注册名单、
`scripts/check-registries.py` 会自动对表（本地先跑 `python3 scripts/check-registries.py --list`）。

### 3. 文档语义改动 → 同步五份副本

同一条语义改动最多要同步五处：`docs/*.md`（gui-guide §12、tutorial、gui-router）、
`website/`（中英双语，en/ 对应页）、`scaffold/template/README.md`、`testdata/*.js` 示例。
改完 Grep 旧关键词确认清零。

### 4. 改 gfx 内核 → 三个不变量不许破

- **纯 Go、零 cgo（桌面侧）**：允许的第三方依赖只有 `x/image`、`jezek/xcb`。
- **单线程确定性**：脚本 / 事件泵 / VM 回调同一 OS 线程串行，跨线程只经 `gfx.Post`。
- **静态子树不可复活**：`dispose` 过的元素重新挂回去只是死树，别制造这种用法。

### 5. 包级 `init()` 不得做平台 IO / 宏内核调用

`init()` 是被链接进来的包**无条件执行**的代码：没有调用点、没有开关、也没有
recover 的机会。往里放一次平台调用，就等于要求**所有**二进制在 `main` 之前先
成功完成它 —— 2026-10-08 的 `gfx/win32` 网卡枚举正是这样把「一个能力崩了」放大
成「进程起不来」（连跑 test262 都启动即 SIGSEGV）。

- 平台调用（syscall / 平台后端包 / 网络）一律挪到**首次真正被用到时**再执行
  （懒加载），或放在注册给用户的回调里 —— 回调是用户调用时才跑的。
- 闸门：`go run ./tools/initpurity ./...`（CI 上单独一个作业）。它会沿 init 的
  **调用链**追到 helper，输出「文件:行号 + 调用链」。
- 确属必要的例外：在违规行加 `// initpurity:allow 理由：…`，理由必填 ——
  没有理由的豁免会被判违规，因为它给后人的是一张空白支票。

### 6. 改了内置模块的导出面 → 重新生成 golden

`docs/exports.golden.json` 是内置模块导出名单的**唯一机器可读真相**（由
`python3 scripts/gen-exports-golden.py` 生成）。导出面属于 API 契约，变化必须
被人看见，所以 CI 只做「实抽 vs golden」diff，**不自动刷新**：

- 新增 / 改名 / 删除一个 `gx/*` 导出 → 跑一次生成脚本，把 golden 与代码
  **放在同一个提交里**（README、官网、npm 包页、脚手架模板都照它更新）。
- CI 红了的正确修法不是删掉 golden，而是确认这次 API 变化是有意的，然后重新
  生成并在 PR 描述里写明。

### 7. 改引擎后：合规率与性能各有一次「不许悄悄变差」

两条闸门都是**趋势型**的，跑在 CI 上而不是本地，规矩也一样：

| 闸门 | 跑的时机 | 基线 | 判据 |
|------|----------|------|------|
| `scripts/check-compliance.py` | test262.yml（周一 03:00 UTC + 引擎核心变更） | `docs/test262-baseline.json` | 用例集没变 ⇒ 合规率跌过 0.5pp 即红；用例集变了 ⇒ 改看通过数跌过 1% |
| `scripts/check-bench.py` | bench.yml（每天 03:17 UTC + 手动） | `bench-results/baseline.json` | 主力判 gox/node **比值**（机器差异被约掉）；绝对值只在机型一致时启用 |

- **基线只在闸门通过后才刷新** —— 所以基线恒等于「上一次通过时的数字」。一次
  回归不会把基线一起拉低，把后面所有轮次洗白。
- 红了的正确修法：**先确认是不是真退化，再决定是修代码还是刷基线**。不要用
  「刷基线」当修 bug 的替代品 —— 那是把警报关掉，不是把火灭掉。
- 上游 test262 加用例导致闸门卡住（不是我们的回归）：手动 dispatch 勾上
  `refresh_baseline` 跳过闸门重设基线；bench 则是 `python3 scripts/check-bench.py
  --update-baseline`。
- 两条闸门都带 `--self-test`（负向用例必须真的红）。改判据时先跑它 ——
  **一个从没失败过的闸门等于黑盒**。

## 移动端 / Android 侧的贡献

- 构建一律走 `bash scripts/build-android.sh`（自动找 NDK + ELF 目标校验）；**桌面 `go test` 看不见 android tag 下的编译错误**，改完必跑该脚本。
- 壳工程在 `app/android/`，验证清单与已知坑见 [app/android/README.md](app/android/README.md)。
- 模拟器验证优先 `--abi x86_64`（x86 主机上原生执行）；真机用默认 arm64-v8a。

## PR 流程

1. 从 `main` 拉分支开发（不要直接提交到 `main`）。
2. 提交前跑一遍上面的本地闸门 + `python3 scripts/check-registries.py`。
3. PR 描述里写清**改了什么、为什么、怎么验证的**（能跑的脚本/命令直接贴）。
4. CI 全绿后 maintainer 会 review；小改动通常 1-2 天内回复。

## 报告安全问题

安全漏洞请勿开公开 issue —— 通过 GitHub Security Advisory 私密报告，
或在 issue 里只描述现象、不放可利用细节。

## 行为准则

参与本项目即同意遵守 [行为准则](CODE_OF_CONDUCT.md)。简而言之：对事不对人，
提问没有蠢问题，保持耐心与尊重。
