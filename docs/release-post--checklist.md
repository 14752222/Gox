# 发布后对外收尾 checklist（GitHub Releases / npm / 官网 三平台口径）

> 看板 **rUkOb1**（文档可做部分）。发布动作（v0.8.0 / v0.9.0 tag、GitHub Release、
> npm publish）已由 `.github/workflows/release.yml` 自动完成；**本文件只收尾"发布之后"
> 的对外口径一致性**。所有需要登录平台账号的操作（改 Release 说明、改包页、改官网）
> 本工作流碰不了，统一列在 §4「待人工」勾选项里。
>
> 口径事实源自：`docs/npm-release.md`（发版手册）、`docs/v1-roadmap.md`（§十五 批次四
> 全量 16809/23726 = 70.85%）、`scripts/build-npm.sh`（五平台编译矩阵）、
> `docs/test262-compliance.json`（徽章）。

## 1. 三平台口径对照表（每发一版填一行）

> 填写原则：**版本号、平台矩阵、合规率三个数字在三处必须逐字符一致**；任何一处先改，
> 另外两处当天跟上。当前最新版：**v0.9.0**（npm `@goxjs/goxjs@0.9.0`）。

| 口径项 | GitHub Releases 说明 | npm 包页（@goxjs/goxjs） | 官网 |
|---|---|---|---|
| 当前版本号 | `v0.9.0`（tag 与 `npm/package.json` version 必须相等，见发版手册 §6「标签与版本号不一致」条） | 包版本 `0.9.0`（`npm view @goxjs/goxjs@latest version` 复核） | 首页/下载页展示的最新版本号 |
| 平台支持矩阵 | Release assets 列表（5 个平台二进制，见 §2） | `npm pack` 产物内的 `binaries/<os>-<arch>/gox[.exe]` 同 5 平台 | 下载页平台勾选列表 |
| 五平台明细 | darwin-x64 / darwin-arm64 / linux-x64 / linux-arm64 / windows-x64 | 同上（`npm/bin/gox.js` 按 `osName + '-' + process.arch` 选二进制） | 同上；**不提供** linux-armv7、windows-arm64 等，别列 |
| 已知缺口（test262） | 70.8% language 合规率（16809/23726，2026-10-06 批次四口径；新版本以当批 A/B 回填） | README「已知限制」小节同数字 | 同左 |
| 已知缺口（语义） | ① class `extends` 仅支持标识符（heritage 箭头/裸计算字段未做，`rO13zU`）；② `await using` 异步释放整体 33.7%（主缺口）；③ module 用例未走真模块入口（`rNR2Zk`，runner 侧）；④ 平台空白见 roadmap §二（Windows/macOS/Linux 桌面之外，Android/iOS 尚在补） | 同左 | 同左 |
| 安装方式 | 文档链 `npm install -g @goxjs/goxjs` | 包页 install 段 | 快速开始 |

**一处一勾**：每次发版后按上表逐格核对，两轮（发布当天 + 一周后抽查）。

## 2. Release assets 的分界（务必写进 Release 说明模板）

- **v0.7.0 及更早的 Release 是 0 assets**（空壳，只有 tag 和一段文字）——
  workflow 的「自动打 tag + 建 Release + 内容校验」是后来才补齐的。
- **v0.8.0 起才真正挂产物**（5 平台二进制 + OIDC attestation）。
- 因此：回溯老版本的用户看到 v0.7.0 及更早「没有下载」不是事故；Release 说明模板里
  带一句「v0.8.0 以前的 Release 不含二进制产物，请使用 v0.8.0+」。

**Release 说明模板**（粘到 GitHub Releases  description 区）：

```markdown
## @goxjs/goxjs v0.9.0

安装：`npm install -g @goxjs/goxjs`

### 平台支持
darwin-x64 / darwin-arm64 / linux-x64 / linux-arm64 / windows-x64（本版起提供全部二进制产物）

### 本版要点
<按 docs/v1-roadmap.md 当批台账填：特性 / 提交 sha / 定向 test262 数字>

### 已知缺口
test262 language 合规率 <当批数字>；class extends 仅标识符；await using 异步释放；
详见 https://github.com/14752222/Gox/blob/main/docs/v1-roadmap.md

> 注：v0.8.0 以前的 Release 不含二进制产物（0 assets），请使用 v0.8.0+。
```

## 3. 三平台各自的填写位置

| 平台 | 在哪里填 | 入口 |
|---|---|---|
| GitHub Releases | repo → Releases → 对应 `v0.9.0` → Edit → description 用 §2 模板 | 网页端（待人工） |
| npm 包页 | `@goxjs/goxjs` 的 README「已知限制」小节随包发布（源头在 **gox-npm 仓库**的 `npm/README.md`，随版本走） | 改 `gox-npm` 仓库（待人工/下次发版带上） |
| 官网 | `website/index.md` / 下载页的版本号与平台矩阵 | 改 website 子模块（待人工） |

## 4. 待人工勾选项（账号操作，本工作流不执行）

- [ ] GitHub Releases：编辑 `v0.8.0` / `v0.9.0` 说明，套 §2 模板（补齐「v0.8.0 起才挂产物」的分界说明）
- [ ] GitHub Releases：核对两个 Release 的 assets 确为 5 平台二进制（不是 0 assets）
- [ ] npm 包页：确认 `@goxjs/goxjs@0.9.0` version / attestation（`/-/npm/v1/attestations/...`，见发版手册 §5）
- [ ] npm 包页：在 `gox-npm` 仓库 README 的「已知限制」小节补三平台一致口径（随下一版发出）
- [ ] 官网：下载页/首页版本号改 v0.9.0、平台矩阵与 §1 一致、补缺口说明
- [ ] 三平台版本号回查一遍（发布后 1 天 + 7 天各一次）

## 5. 掘金 / 知乎互挂（依赖 `rcfa8R`，本条不阻塞）

- 状态：**依赖看板 `rcfa8R`（宣传稿未发）**。稿子发布前，三平台文档只管口径一致，
  **不必**提前在 Release 说明 / 包页 / 官网挂文章链接。
- 稿子发布后的互挂动作（同样列待人工）：
  - [ ] 掘金文章底部挂 npm 包页 + 官网链接
  - [ ] 知乎想法/文章挂同一组链接
  - [ ] GitHub Releases 说明「本版要点」末尾加文章链接（可选）
- 本条与 §1–§4 无依赖顺序，`rcfa8R` 落地后单独走一节即可。
