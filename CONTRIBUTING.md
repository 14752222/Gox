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

## 第一次 PR 演练

先在 GitHub fork `14752222/Gox`，再把下面的 `YOUR_NAME` 换成你的用户名：

```bash
git clone --recurse-submodules https://github.com/YOUR_NAME/Gox.git
cd Gox
git remote add upstream https://github.com/14752222/Gox.git
git switch -c fix/issue-NUMBER

# 完成并检查改动
gofmt -l .
go build ./...
go vet ./...
go test ./...
python3 scripts/check-registries.py --list

git add <改动的文件>
git commit -m "fix(scope): 简述改动"
git push -u origin fix/issue-NUMBER
```

最后从 fork 的分支向 `14752222/Gox:main` 开 PR。CI 变红时，先打开失败的 job
查看第一条错误，并在本地重跑上面的对应命令；注册表一致性失败时重点查看
`python3 scripts/check-registries.py --list` 的输出。

`website/`、`npm/`、`gox-logo-concepts/` 是主仓记录提交指针的三个子模块。
通常只提交主仓里的指针更新，不要为同一次主仓改动另给子模块开 PR；克隆后若目录为空，运行：

```bash
git submodule update --init --recursive
```

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
