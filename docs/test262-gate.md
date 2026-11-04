# Test262 合规率口径与批次地板（C-0 核销 / C-5）

> 状态：2026-11-04 拍板口径已生效。本文档是合规率数字的**唯一权威口径**，
> 徽章、基线、CI、台账、对外汇报一律以本文为准。

## 一、拍板口径（2026-11-04）

- **暂缓冲 95%**。95% 是「几乎全量语义对齐」的量级，长周期（6 个月以上），
  先按分档目标长跑。
- 分档目标（同时是批次地板的初始值，见 `docs/test262-gate-floors.json`）：

| 口径 | 分母 | 目标（地板） |
|------|------|--------------|
| language | language 套件实际收集用例 | **85%** |
| built-ins | built-ins 套件实际收集用例 | **50%** |
| 全量（all） | language + built-ins 两套件合计 | **80%** |

- **明确不做**：Intl、Atomics、SharedArrayBuffer、BigInt（docs/test262-builtins-clusters.md
  §四与 v1-roadmap 已论证）。它们对天花板的压制已在拍板时接受。
- 资源排序：与性能、GPU 抢资源时，合规率**排第三**。
- 节奏：每两周一个归因批次；批次结束必出全量 A/B（三项 diff 全 0 才算零回归）
  与台账更新；批次通过后抬升批次地板。

## 二、「95%」绑定哪个分母

原 C-0 冲突（徽章 language 97.0% vs 基线 85.58%）的根因：徽章与基线是
**两套独立链路各写各的**。现在已在 test262.yml 里钉死为单链路：

```
gox test262 -json results.json          # 一次跑批
  ├→ 徽章   docs/test262-compliance*.json   ← results.rate_pct（同一次跑批）
  ├→ 基线   docs/test262-baseline*.json     ← 同上（闸门通过后刷新）
  └→ 地板   docs/test262-gate-floors.json   ← 只升不降，批末手动抬
```

徽章 = 基线 = CI 输出，三处同源，**都是同一次跑批的同一份 results JSON**。
「95%」若重启，其分母沿用本表：built-ins 口径 23861 例、全量口径 47693 例
（分母随 test262 上游滚动，以当次 results 的 total 为准）。

## 三、复现命令（任何数字必须可用它重算）

```bash
# 1. 构建（commit 必须记录在案）
go build -o /tmp/gox ./cmd/gox
git log -1 --format=%H

# 2. 取用例（浅克隆或快照，记录 test262 的 commit/日期）
git clone --depth 1 https://github.com/tc39/test262.git /tmp/test262

# 3. 跑批（两个套件分开跑，各自一份 results）
/tmp/gox test262 -root /tmp/test262 -suite language  -jobs $(nproc) -json /tmp/lang.json
/tmp/gox test262 -root /tmp/test262 -suite built-ins -jobs $(nproc) -json /tmp/bi.json

# 4. 读数（rate_pct 即徽章与基线的数字来源，逐位一致）
python3 -c "import json;d=json.load(open('/tmp/lang.json'));print(d['rate_pct'],d['passed'],d['total'])"
```

## 四、三层防线（防边涨边退）

1. **相对闸门**（每次跑批）：现值 vs 上次基线，掉过 0.5pp 判红；
2. **批次地板**（每次跑批）：绝对下限，初始 = 拍板口径（85 / 50 / 80），
   用例集变动时自动降级为通过数判据（与相对闸门同一套防噪逻辑）；
3. **全量合并地板**（合并 job）：language + built-ins 合并率 ≥ 80%。

### 批末抬地板（两周一批）

```bash
python3 scripts/check-compliance.py \
  --current test262-results-language.json \
  --floor-file docs/test262-gate-floors.json --raise-floor
```

新地板 = max(旧地板, 本批实际水平 − 0.3pp 容差)，**只升不降**；每次抬升写入
floors 文件的 `history` 台账（含批次 commit 与前后值）。CI 里对应
test262.yml 的 `workflow_dispatch` 勾选 `raise_floor`。

### 台账标注规则（C-5）

PASS 转 FAIL 的类例（补齐缺失 API 后暴露的真实缺陷）在批次台账中**单独归类，
不计入回归**——那是遮羞布被揭，不是退步。

## 五、历史快照

| 日期 | commit（Gox） | test262 | language | built-ins | 全量 | 备注 |
|------|---------------|---------|----------|-----------|------|------|
| 2026-11-04 | eee3d21 | 每周 CI | 86.32%（20571/23832） | 75.06%（17910/23861） | 80.68%（38481/47693） | CI 徽章与基线同源 |
| 2026-11-04 | 7de292e | main @ 831359a（2026-11-04 快照） | 86.3167%（20571/23832） | 75.0765%（17914/23861） | 80.6932%（38485/47693） | 现编现跑核销（C-0）：language 与基线逐位一致，三处同源对齐；built-ins 较基线 +4 例（正向漂移，非回归）；批次地板（85/50/80）全绿建立 |
| 2026-11-04 | harness 登记批次（见提交） | main @ 831359a（2026-11-04 快照） | 86.3125%（20570/23832） | 75.0765%（17914/23861） | 80.6904%（38484/47693） | harness 未登记清零（C-3）：built-ins skipped 185→0（atomicsHelper.js/testAtomics.js/nativeErrors.js），language skipped 34→0（tcoHelper.js）。A/B 零回归（language −1 例为既有分片私有名污染抖动用例，隔离复跑通过，见 C-3 台账）；Atomics 用例由「跳过」转为「真实执行按语义失败」，失败形态可归因 |
