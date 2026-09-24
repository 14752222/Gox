# @goxjs/goxjs 发版手册（npm）

> **一句话**：改 `npm/package.json` 的 `version` → 提交 → push 到 `main`。剩下的由
> `.github/workflows/release.yml` 全自动完成：判重（版本已在 registry 上就跳过）→
> 注册表一致性校验（内置组件四处 / gx 模块三处 / 版本号 / npm 清单）→ 交叉编译
> 五平台二进制 → 打包内容校验 → OIDC 发布 → 打 tag + 建 Release。
> **本机不需要 token、不需要 `npm login`、不需要手工 `npm publish`。**

流水线自身的注释里也有同一套说明（`.github/workflows/release.yml` 文件头），改动请两边同步。

---

## 1. 版本号怎么定

| 变更类型 | 例子 | 版本动作 |
|---|---|---|
| 破坏性（删/改公共 API） | 0.2.0 → 0.3.0：`<For>` / `<Show>` 组件移除，改用 `each` / `show` 指令 | **minor +1**（0.x 下 minor 即破坏位） |
| 新能力（向后兼容） | 新增 `model` 指令、新增 `view` 标签 | minor +1（与上一条同理，0.x 阶段合并处理） |
| 纯修 bug / 文档 | 注释、README、workflow 措辞 | patch +1 |
| 只改仓库其它部分 | 与 npm 包内容无关 | **不动版本号**（推 main 只会走一次十几秒的判重） |

**判据**：`npm view @goxjs/goxjs versions` 里没有的版本号才会被发布 —— 所以"改了版本号并推 main"
就等于发版，"没改版本号推 main"绝不会误发。

## 2. 发版前要同步的文档（本次 0.2.0 → 0.3.0 的清单）

> 先跑一遍静态闸门，它会替你抓出"改了 `main.go` 的版本号没改 `package.json`"这类
> 手工同步遗漏（CI 里是 `release.yml` 的第一道步骤，本地等价命令）：
>
> ```bash
> python3 scripts/check-registries.py          # 四项静态一致性
> python3 scripts/check-registries.py --list   # 只对表，不判定
> ```
>
> 新增/删改 Gox 内置标签或 `gx/*` 模块之后，还要跑一次负向自测
> （`python3 scripts/check-registries-selftest.py`）—— 确认这把闸门还会红。

| 文件 | 改什么 | 为什么 |
|---|---|---|
| `npm/README.md` | **必改**：新 API 示例、破坏性变更说明 | 它是 npm 包页面，用户看到的第一份文档；CI 的内容校验也要求包里必须有 `README.md` |
| `npm/package.json` | `version`；必要时 `description` / `keywords` | `version` 是唯一发版开关 |
| `main.go` 的 `const version` | 与上一条的 `version` 一起改 | `gox version` 报的就是它；两处不一致时排障会先被版本号误导 |
| `README.md`（仓库） | 特性清单、GUI 指引段落 | 仓库首页 |
| `docs/gui-guide.md` | §4 元素表（新标签）、§6 绑定、§8 视图、§11 示例索引 | 用户手册 |
| `docs/gui-patterns.md` | §9 视图 | 模式手册 |
| `docs/gui-model-binding.md` | 接口设计与迁移对照 | 设计说明书 |
| `agent_doc/gui-component-status.md` | 追加「落地记录」小节（本文档的 §32 / §33） | 权威现状记录 |
| `website/components.html` | `#view` 段（指令表）、受控组件约定 ③、限制清单 | 官网组件参考 |
| `website/api.html` | 聚合入口 `gox` 示例、`gx/view` 导出表 | 官网 API 页 |
| `website/guide.html` | 响应式章节里的受控/绑定说明 | 官网入门页 |

`website/index.html` 与 `guide.html` 的其余部分不涉及这些 API，实测无需改动。

## 3. 一次发版的完整流程（可直接复制）

```bash
# 0) 站在 main 且工作区干净
git status --porcelain            # 只该有你这次要提交的改动
git log --oneline -1

# 1) 改版本号（0.3.0 / 0.3.1 …）与包 README
$EDITOR npm/package.json npm/README.md

# 2) 纪律自检：.gitignore 绝不能命中包内容
#    被命中 ⇒ 该文件即使写在 package.json 的 files 里也进不了包（0.2.0 发过空壳包的根因）
for p in npm/package.json npm/bin/gox.js npm/README.md npm/.npmignore; do
  git check-ignore -q "$p" && echo "!! 被忽略: $p" || echo "ok(未忽略): $p"
done

# 3) 本地预检（可选但强烈建议，几分钟）：编二进制 → 打包 → 核对包内容
bash scripts/build-npm.sh --into-package
( cd npm && npm pack --pack-destination ../dist/npm-pack )
tar -tzf dist/npm-pack/goxjs-goxjs-*.tgz | sort      # 必须看到 5 个平台的 binaries/ + README.md
rm -rf npm/binaries                                  # 预检产物别留在工作区（CI 里是 always() 清）

# 4) 提交 + 推送 —— 这一推就是"发版"（版本号在 registry 上不存在时才会真发）
git add npm/package.json npm/README.md README.md docs/ website/ \
        .github/workflows/release.yml   # 只 add 自己这次动的文件, 别用 -A
git commit -m "chore(release): 0.3.0"
git push Gox main:main

# 5) 验收（1 分钟内完成；registry 有边缘缓存，查太早会看不到）
curl -s "https://registry.npmjs.org/@goxjs%2Fgoxjs/0.3.0" | head -c 400
curl -s "https://registry.npmjs.org/-/npm/v1/attestations/@goxjs%2Fgoxjs@0.3.0" | head -c 400
```

## 4. 配置要求（一次性，都在网页端/仓库设置里）

**npm 侧 —— Trusted Publishing（OIDC），四字段逐字符相等：**

| 字段 | 值 |
|---|---|
| Organization or user | `14752222` |
| Repository | `Gox` ← **大写 G**（npm 做大小写敏感精确匹配） |
| Workflow filename | `release.yml` |
| Environment name | 留空（workflow 里没有 `environment:`） |

失配的症状是**误导性的 404**（不是 403）：看着像"包不存在"，实际是身份没对上。

**workflow 侧的红线（已写死在 `release.yml` 里，别"顺手优化"掉）：**

- `permissions: id-token: write` —— 换 OIDC 凭据必需；`contents: write` —— 自动打 tag / 建 Release 必需。
- **不得出现 `NODE_AUTH_TOKEN`**，`actions/setup-node` **不得写 `registry-url`**：
  它会让 setup-node 往 `.npmrc` 塞一行占位 token，npm 据此认为"要走 token 认证"从而
  **跳过 OIDC 交换**，最后也是那个 404。
- 发布前必须把 npm 升到 **≥ 11.5.1**（Node 22 自带 10.x，workflow 里有一步 `npm install -g npm@latest` 并卡版本）。
- 打包布局三条纪律（0.2.0 空壳包的教训）：**产物不进包目录**（编到 `dist/npm/binaries/` 再复制）、
  **包目录放占位 `.npmignore`**、**`.gitignore` 里不出现 `npm/` 下任何路径**。

**本地侧**：`gh` CLI / GitHub token 都不是必需的（发版由 CI 完成）。本机 git 远端名是 `Gox`
（不是 `origin`），且本机 git 不支持多级引用，一律 `git push Gox main:main`。

## 5. 验收口径（不看 CI 颜色，看 registry）

1. `npm view @goxjs/goxjs@<ver> version` 能查到；
2. `/-/npm/v1/attestations/@goxjs%2Fgoxjs@<ver>` 里出现 **`attestations`**（`publish/v0.1` +
   `slsa.dev/provenance/v1`）—— 这才证明是 OIDC 发布的；只有 `signatures` 说明是旧 token 手工发的；
3. 包里内容对：`npm pack` 出来的 tgz 用 `tar -tzf` 列出 **5 个平台二进制 + package.json + README.md**；
4. GitHub 上出现 `v<ver>` 标签与 Release（push main 发布成功后由 workflow 自动创建）。

## 6. 出问题怎么查

- **本机没有 `gh` / token，读不了 job 日志（未登录 403）**，但公开仓库的
  **check-run 注解匿名可读**：
  `GET /repos/14752222/Gox/actions/runs?per_page=1` → run id →
  `GET /repos/14752222/Gox/actions/runs/<id>/jobs`（每步 status + `check_run_url`）→
  `GET /repos/14752222/Gox/actions/runs/<id>/jobs/<job_id>/annotations`（或 check-runs 路径）拿 `::error::` 全文。
  所以 workflow 里每个校验步骤都自己 `echo "::error::<带证据的消息>"`（把实际文件清单/原始输出塞进去），
  否则失败时只剩一句 `exit code 1`。
- **想重跑**：推一次 main，或重建标签（`git push Gox :refs/tags/vX.Y.Z` 再 push）；界面上
  的 Re-run 也需要 token。
- **标签与版本号不一致**：workflow 会显式报错退出（tag push / Release 事件以 ref 上的版本为准，
  且必须等于 `npm/package.json` 的 version）。发版时只推 main 就不会碰到这条。
- **判重命中**：日志里是 `::notice::… 已在 registry 上，跳过发布`，不是失败 —— 重推、重跑历史
  run、补跑失败的发版都不会误发。

## 7. 相关文件

- `.github/workflows/release.yml` —— 流水线本体（文件头有同一份说明）
- `.github/workflows/ci.yml` —— 常规闸门：注册表一致性 + `go build`/`vet`/`test`
- `scripts/check-registries.py` —— 四项静态一致性（内置组件四处 / gx 模块三处 / 版本号 / npm 清单）
- `scripts/check-registries-selftest.py` —— 上一条的负向自测，证明它真的会红
- `scripts/build-npm.sh` —— 五平台交叉编译 + 可选 `--into-package`
- `npm/package.json` / `npm/bin/gox.js` / `npm/README.md` —— 仓库跟踪的包定义三件套
- `agent_doc/gui-component-status.md` —— 每次能力落地后追加的「落地记录」
