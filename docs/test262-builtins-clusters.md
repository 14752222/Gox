# built-ins 合规洼地聚类报告（rnm4C5）

**结论先说**：built-ins 套件实测 **33.42% → 35.69%**（本次顺带修的 `new C(...args)` /
`new obj.Ctor(1)` 引擎缺陷贡献 +541 例）。**失败面不是"一万五千条零散 bug"，而是
522 个簇里的 10 个就占了失败面的 42.75%**——排期应当按簇开单，而不是按条修。

| 指标 | 数字 |
|------|------|
| 套件 | `built-ins`（23823 例，全量跑完，覆盖度 100%） |
| 通过 / 失败 / runner 跳过 | 8503 / 14951 / 369 |
| **实测合规率** | **35.6924%** |
| 修复前（6285a0b 原样，同参数实测） | 33.4200%（7962/23823） |
| 本次引擎修复净收益 | +541 例 / +2.27pp（含 7 条退潮，见文末遗留） |
| 跑批耗时 | 62.7s（`-jobs 16`，128 固定分片） |

> 口径：`gox test262` 的分母是"实际收集到的用例"，runner 侧跳过的（harness 未登记）
> **计入分母不计入通过**——所以 369 条 harness 依赖既是失败面，也是最便宜的一块。

---

## 一、失败形态：先分形态，再看目录

形态由 `test262Result` 的 `phase` + `error` 两个已有字段判定（判据与关键词见
`scripts/test262-cluster.py` 文件头，脚本带 `--self-test` 钉住判据），**互斥**，
各形态之和 = 失败总数 15320。

| 形态 | 规模 | 占失败面 | 占全量 | 排期含义 |
|------|-----:|--------:|-------:|----------|
| 语义偏差 | 9694 | 63.28% | 40.69% | 能力在，值/顺序/边界/this 校验与规范不符 —— **主战场**，逐个方法对齐规范 |
| 功能零实现 | 5185 | 33.84% | 21.76% | 停在编译前端或运行期报"没有这个东西" —— **成块补实现**，修完整段搬走 |
| harness 依赖 | 369 | 2.41% | 1.55% | 用例根本没进引擎 —— **runner 侧**，成本最低，先清 |
| crash | 52 | 0.34% | 0.22% | 超时挂死 —— 优先级最高，它会污染整个分片 |
| 假阳性退潮 | 20 | 0.13% | 0.08% | negative 用例判定失配 —— 量小但信号真（全部落在 RegExp/property-escapes） |

一句话读法：**六成是"做得不对"，三成四成是"没做"**。这决定了排期的形状：语义偏差
要按方法逐个对齐（慢、但每条都是真合规），功能零实现要按能力整块补（快、但需要先
决定做不做——典型就是 Intl）。

---

## 二、Top 10 簇（目录族 × 失败形态）

族 = 用例所在目录的前 3 段。每簇一条可直接喂 `gox test262 -filter` 的正则。

| # | 目录族 | 失败形态 | 簇规模 | 占失败面 | 族基线（通过/总数） | 定向 filter |
|---|--------|---------|-------:|--------:|--------------------|-------------|
| 1 | `built-ins/Array/prototype` | 语义偏差 | 1708 | 11.15% | 967/2812 = 34.39% | `^built-ins/Array/prototype/` |
| 2 | `built-ins/TypedArray/prototype` | 功能零实现 | 1311 | 8.56% | 0/1411 = **0.00%** | `^built-ins/TypedArray/prototype/` |
| 3 | `built-ins/Temporal/ZonedDateTime` | 语义偏差 | 605 | 3.95% | 277/901 = 30.74% | `^built-ins/Temporal/ZonedDateTime/` |
| 4 | `built-ins/Temporal/PlainDateTime` | 语义偏差 | 531 | 3.47% | 226/773 = 29.24% | `^built-ins/Temporal/PlainDateTime/` |
| 5 | `built-ins/Temporal/PlainDate` | 语义偏差 | 452 | 2.95% | 184/652 = 28.22% | `^built-ins/Temporal/PlainDate/` |
| 6 | `built-ins/RegExp/property-escapes` | 功能零实现 | 447 | 2.92% | 143/613 = 23.33% | `^built-ins/RegExp/property-escapes/` |
| 7 | `built-ins/String/prototype` | 语义偏差 | 391 | 2.55% | 571/1073 = 53.22% | `^built-ins/String/prototype/` |
| 8 | `built-ins/Temporal/PlainYearMonth` | 语义偏差 | 374 | 2.44% | 127/509 = 24.95% | `^built-ins/Temporal/PlainYearMonth/` |
| 9 | `built-ins/Temporal/PlainTime` | 语义偏差 | 372 | 2.43% | 117/493 = 23.73% | `^built-ins/Temporal/PlainTime/` |
| 10 | `built-ins/Temporal/Duration` | 语义偏差 | 358 | 2.34% | 172/540 = 31.85% | `^built-ins/Temporal/Duration/` |

Top 10 合计 6549 条，占失败面 42.75%（522 个簇里的 10 个，占四成以上）。补两条观察：

- **Temporal 是"一族七个簇"**：Top 10 里 6 个（#3/#4/#5/#8/#9/#10）、Top 12 里 7 个
  都是 Temporal 的不同类型目录，合计 **1360/4605 = 29.53%**（失败 3245，占失败面
  21.18%）。按目录切开会把它拆成 7 张单，**同一套实现面对 7 次**——这正是 language
  阶段"同一机制修两次"的翻版。所以它应当合并成一张单（见 Top 3 建议 #1）。
- **cluster #2 的族基线是 0.00%**（1411 条全灭）。全族零通过不是"偏差"，是"门没开"：
  1306 条报 `Cannot read properties of undefined (reading 'resize')`（resizable
  ArrayBuffer / `%TypedArray%.prototype.resize` 未实现），另有 178 条卡在 harness
  `resizableArrayBufferUtils.js` 未登记。这类簇的投入产出比通常最高——分母大、当前
  值 0、根因单一。

### harness 依赖（369 条，最便宜的一块）

| harness | 条数 |
|---------|-----:|
| `resizableArrayBufferUtils.js` | 178 |
| `atomicsHelper.js` | 112 |
| `testAtomics.js` | 52 |
| `nativeErrors.js` | 21 |
| `iteratorZipUtils.js` | 6 |

登记 harness 只是把用例放进引擎，**不等于通过**；但它把"不知道为什么失败"变成
"知道为什么失败"，且 `resizableArrayBufferUtils` / `atomicsHelper` 两块与 Top 10
的 #2 同源，可以并进同一张单。

### crash（52 条超时挂死）

集中在 `built-ins/Array/prototype`(13)、`built-ins/Array/length`(6)、
`built-ins/Object/defineProperties`(6)、`built-ins/Object/defineProperty`(5)、
`built-ins/JSON/stringify`(4)。量小但**必须先修**：超时的 VM goroutine 泄漏会触发
引擎包级状态的 fatal，整个分片的成果都会丢。

---

## 三、排期建议（按形态定策略，不按目录名猜）

1. **先清 crash（52）**：稳定性优先，它会污染整轮跑批的可信度。
2. **再清 harness（369）**：runner 侧白名单 + 对应能力，成本最低、信息量最大。
3. **然后打功能零实现里"全族 0%"的簇**：`TypedArray/prototype`（0%）、
   `RegExp/property-escapes`（23.33%，且它的 20 条 negative 用例就是全部的
   "假阳性退潮"——`\p{...}` 非法属性转义没被解析器拦住）。这类是"整块能力缺失"，
   补完能整段从失败面搬走。
4. **语义偏差按方法族逐个推进**：`Array/prototype` → Temporal 全族 →
   `String/prototype`。这条线慢，但每条都是真合规。
5. **Intl 先拍板（见下）**：它决定 built-ins 的天花板，不拍板就会反复被问。

---

## 四、Intl 边界建议：**不做 Intl**（建议拍板为"不做"，并写下理由）

| 依据 | 数字 |
|------|------|
| `intl402` 套件是否在口径内 | **不在**：`suiteDirs` 只有 language/built-ins/annexB；（本次用的 test262 checkout 里 `test/intl402` 目录根本不存在） |
| built-ins 中**路径**命中 Intl 关键字的用例 | 205 条（占全量 0.86%，占失败面 0.91%），通过 65，合规率 31.71% |
| built-ins 中**源码真的引用** `Intl` 的用例 | **11 条**（集中在 Temporal 的日历/时区分支：PlainDate 4、ZonedDateTime 3、PlainDateTime 3、Array/prototype 1） |
| 其失败形态 | 功能零实现 93 / 语义偏差 41 / harness 6 |

**建议：不做。** 三条理由：

1. **量级不成立**：Intl 真正卡住的只有 11 条 built-ins 用例（0.05%），而 Intl 需要
   CLDR 数据与 ICU 级别的本地化实现，是 Gox 目前**唯一一个"要引入一整个数据生态"
   的领域**。
2. **口径一致性**：runner 的既定取舍是"未实现的领域不得灌大分母"（见
   `cmd_test262.go` 文档头）。把 intl402 收进来会让 built-ins 之外的数字失真，也会
   让新加的 built-ins 下限闸门失去意义——上游加一批 intl402 用例就能把合规率打下去
   几个百分点，那是噪声不是回归。
3. **收益可以延后而不丢**：205 条里绝大多数是 `toLocaleString` / `localeCompare`
   的"存在性"用例，它们挂在 `String/prototype`、`TypedArray/prototype` 等族里，
   会随各自族的主修复一起动；不需要单开 Intl。

**若将来必须碰**（Temporal 推到日历/时区那一步时）：只做 `Intl.DateTimeFormat` 的
最小面——`resolvedOptions().timeZone` 一个取值，覆盖量级 11 条。**不做** `Collator`、
`NumberFormat`、`PluralRules`、`Segmenter`、`ListFormat`、`RelativeTimeFormat`、
`DisplayNames`。

---

## 五、Top 3 簇立项建议（三张单的标题 / 范围 / 定向 filter / 基线）

> 基线数字全部来自本次实测（`built-ins` 全量，23823 例，2026-10-10）。
> 开单口径：**一簇一单，验收看族基线**——不是"修了几条"，而是"这族的合规率涨了多少"。

### 单 1 ｜Temporal 全族语义对齐（最大的一块，七个簇同源）

- **范围**：`built-ins/Temporal/**` 全部 9 个顶层子目录 —— 8 个类型
  （PlainDate / PlainDateTime / PlainTime / PlainYearMonth / PlainMonthDay /
  Duration / Instant / ZonedDateTime）+ `Now`。
  失败形态以语义偏差为主（参数校验顺序、`instanceof` 与原型装配、options 袋的类型
  与顺序、`RangeError` vs `TypeError` 的选用）。
- **定向 filter**：`-filter '^built-ins/Temporal/'`
- **基线**：**1360/4605 = 29.53%**（失败 3245，占 built-ins 失败面 21.18%）
- **为什么合并**：Top 10 里 6 个簇、Top 12 里 7 个簇都是它，拆成 7 张单等于同一套
  实现面对 7 次。
- **验收**：`^built-ins/Temporal/` 合规率 ≥ 50%（+20pp ≈ +920 例）。

### 单 2 ｜Array.prototype 泛型算法：this 校验过严与类数组接收者

- **范围**：`Array.prototype.{map,filter,forEach,some,every,reduce,reduceRight,
  indexOf,lastIndexOf,...}` 的 `this` 校验策略 —— Gox 当前对类数组接收者统一抛
  `called on incompatible receiver object`，而规范要求走泛型路径（读 `length` +
  索引属性）。全量 **1247 条**报这个错，其中 **1016 条**落在
  `built-ins/Array/prototype`（reduce 126 / reduceRight 124 / filter 99 / some 97 /
  every 96 / map 93 / forEach 90 / indexOf 88 …），其余散在 String/Number/Set 的
  同名泛型方法上 —— 同一段"泛型算法 this 校验"代码，建议一并带走。
- **定向 filter**：`-filter '^built-ins/Array/prototype/'`（窄口径可加
  `-filter '^built-ins/Array/prototype/(map|filter|forEach|some|every|reduce)'`）
- **基线**：**967/2812 = 34.39%**（失败 1845，最大单簇 1708）
- **验收**：`^built-ins/Array/prototype/` 合规率 ≥ 60%。

### 单 3 ｜TypedArray.prototype 全族开门：resizable ArrayBuffer + resize 族

- **范围**：`%TypedArray%.prototype.{resize,length,byteLength,...}` 与 resizable
  ArrayBuffer；含 runner 侧登记 `resizableArrayBufferUtils.js`（178 条）、
  `atomicsHelper.js`/`testAtomics.js`（164 条，同属 Atomics 与 SharedArrayBuffer
  的"零实现"面，可一并带走）。
- **定向 filter**：`-filter '^built-ins/TypedArray/prototype/'`
- **基线**：**0/1411 = 0.00%**（全族零通过，占失败面 8.56%）
- **为什么优先**：分母大、现值 0、根因单一（1306 条是同一个 `resize` 未实现），
  是三张单里投入产出比最高的。
- **验收**：`^built-ins/TypedArray/prototype/` 合规率 ≥ 30%，且 harness 未登记
  归零。

**备选第 4 张（Top 10 内、未进 Top 3）**：`built-ins/RegExp/property-escapes`
（143/613 = 23.33%，447 条失败）—— Unicode 属性转义 `\p{...}` 的解析与属性表，
且它是"假阳性退潮"唯一的一簇（20 条 negative 用例要求解析器拦住非法属性转义）。
不进 Top 3 的原因：它是单点语法能力，与另外三张的"族级杠杆"不在一个量级。

---

## 六、复现命令

```bash
# 1) built-ins 全量跑批（约 60s @ -jobs 16）
gox test262 -root /workspace/test262 -suite built-ins -jobs 16 -json /tmp/builtins.json -quiet

# 2) 聚类（脚本可重复执行、输出确定；--depth/--top 可调）
python3 scripts/test262-cluster.py --results /tmp/builtins.json --top 10
python3 scripts/test262-cluster.py --results /tmp/builtins.json --json /tmp/c.json --md /tmp/c.md
python3 scripts/test262-cluster.py --self-test          # 判据自检

# 3) 合规率下限闸门（built-ins 用自己的基线文件）
python3 scripts/check-compliance.py --current /tmp/builtins.json \
                                    --baseline docs/test262-baseline-builtins.json
```

---

## 七、遗留与假设

1. **形态判定是启发式**：「功能零实现 vs 语义偏差」的边界没有唯一答案，脚本用
   错误文本关键词切分（关键词集中在 `MISSING_MARKERS`）。改判据必须同步
   `--self-test`。簇的**规模排序**对关键词不敏感（前 10 簇的形态标签人工抽查过），
   但形态总量会随判据浮动几个百分点。
2. **引擎修复的 7 条退潮**（built-ins 6 + language 1）已知且同源：5 条是
   `Expected a TypeError ... but no exception was thrown`，根因是
   **Gox 的 `new` 没有做 IsConstructor 校验**——旧的错误解析把 `new obj.method()`
   变成 `(new obj).method()`，靠"new 一个普通对象报 TypeError"**偶然**通过；解析修
   正后这 5 条暴露真实缺陷。另 2 条是实参真正到位后抛错顺序/类型变化
   （`Proxy/construct/null-handler`、`Temporal/Duration/invalid-type`）。
   **建议单独开一张补 IsConstructor 的单**（`OP_NEW` 对 `IsMethod`/`IsArrow`/
   非构造器内建抛 TypeError），不在本单赌引擎大改。
3. **Intl 的 205 条是路径命中口径**（含 `toLocaleString`/`localeCompare`），
   比"源码引用 `Intl` 的 11 条"宽——两个数都列出来，避免任一被单独引用时失真。
4. **跑批环境**：test262 checkout `2e0a5676`（2026-10-08），只有 `built-ins` 与
   `language` 两个套件目录。分片数固定 128、与 `-jobs` 解耦（引擎有包级状态，
   用例判定取决于同进程邻居）——换 `-jobs` 重跑，数字可能有小幅抖动，跨机对比时
   要盯 `engine` 段的 commit。
5. **`go test ./vm/...` 在本机偶发 `signal: killed`**：6285a0b 原样（A/B 的 base）
   与改动后同样概率出现，逐用例 verbose 跑又是绿的 —— 判定为**既有的进程级
   flake**，非本单引入；`parser` / `compiler` / `bytecode` / `object` 包与 vm 的
   定向子集（New/Spread/Construct/Call/Method/Class/Super）全绿。CI 上要留意它
   是否与本单改动混在一起报错。
