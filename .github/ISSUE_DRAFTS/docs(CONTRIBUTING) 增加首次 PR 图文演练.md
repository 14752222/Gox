labels: ["good first issue"]
### 要解决什么问题

CONTRIBUTING.md 已上线，但首次贡献者（尤其不熟悉 SSH remote / 子模块布局的人）
最常卡在：fork 后子模块是空的、`git push` 目标是 fork 而非本仓库、CI 的
「注册表一致性」闸门红了自己不知道跑什么命令。

### 期望的产出

1. CONTRIBUTING.md 增加「第一次 PR 演练」小节，覆盖：
   fork → clone（含 `--recurse-submodules`）→ 建分支 → 改动 → 本地闸门 →
   push 到 fork → 开 PR → CI 红了怎么查（`python3 scripts/check-registries.py --list`）；
2. 明确本仓库的三个子模块（`website/`、`npm/`、`gox-logo-concepts/`）与主仓的
   指针关系（不为子模块单独开 PR）；
3. 用真实命令序列（可复制粘贴）而非描述性语言。

### 验收

- 一个新 contributor 按步骤能无提问完成首次 PR（找同事/朋友试跑最好）。

---

参与方式见 [CONTRIBUTING.md](https://github.com/14752222/Gox/blob/main/CONTRIBUTING.md)。
