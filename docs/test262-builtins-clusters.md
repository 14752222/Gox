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

---

# 假阳性清扫：缺失 API 误抛骗过 assert.throws 的类例清单（T1-4 / rm1Dow）

**结论先说**：built-ins 23823 例里，有 **128 条 PASS 是"遮羞布"** —— 用例靠
assert.throws 期待一个 TypeError，而引擎因**缺失 API**（期望的方法/访问器解析成
undefined，进而在 undefined 上调用或读属性）恰好抛出同类型错误，断言被"骗过"，
用例的规范语义从未被真正检验。它们散在 **13 个簇**里，每簇都能用一个已实测确认
缺失的 API 名字钉住。补齐任何一簇的实现时，这批用例将从"判无效应区"进入"真检验
区"——那时出现的 FAIL 是**遮羞布被揭，不是回归**，台账按 §五的规则标注。

## 一、探针方法（可复现）

runner 新增 `-probe` 模式（`cmd/gox/cmd_test262.go`，随本节同批提交）：在 harness
之后注入 assert.throws / assert.throwsAsync 包装器，把每次"骗过断言"的真实错误
（constructor 名 + message）记入 `globalThis.__fp_hits`，仅在 PASS 且 `-probe` 时
写入结果的 `fp_hits` 字段。包装器保持原断言语义（断言失败照常抛 Test262Error），
**探针只观察，不改判**。

分片模型与常规跑批完全一致（128 固定分片、片内同序），因此可与基线逐用例对账：

- 用例集一致（探针首轮曾把独立工具崩溃残留的 16 个 `.gox-fp-*` 临时文件误收集，
  已清理并把 runner 的临时文件前缀判据之外残留说明保留在此存照）；
- **PASS→FAIL 翻转 0 条**（探针无干扰）；
- FAIL→PASS 3 条：单例隔离复跑 2 过 1 败，判定为既有的"同进程邻居敏感" flake
  （见上文 rnm4C5 段 §七.5 的同类现象），非探针引入。

## 二、总量数字

| 指标 | 数字 |
|------|------|
| 套件 / 引擎身份 | `built-ins` @ 源码快照 `7c9150a3`（与 `cdb2a4fb` 引擎代码零差异，后者仅 bench 归档）；本机编译，`vcs_revision` 空以 commit 说明为准 |
| 基线通过 / 失败 | 14803 / 8829（**62.14%**）；探针跑批 14806（+3 flake） |
| assert.throws 通道捕获到真实抛错的 PASS 用例 | 3753 例 / 50382 次 hit |
| 其中"undefined 信号"（期望的 API 解析成 undefined/null） | 222 例 / 669 hit |
| 归入确证缺失 API 的簇（**Tier A**） | **128 例 / 13 簇** |
| 未归簇（复核后绝大多数为真通过：测试故意传非函数） | 94 例 |

判据分层：

- **Tier A（确证假阳性，128 例）**：PASS + 捕获错误为 `undefined|null is not a
  function/constructor` 或 `Cannot read properties of undefined|null (reading 'X')`
  + 源码引用该 API + gox 内 `typeof` 实测确认缺失。四条判据全满足才入列。
- **Tier B（机制性偏差、可观察一致）**：抛错机制与规范不符但异常类型恰好一致
  （典型：`iterator.next.call(map)` 在"内部槽校验"与"next 不存在"下都抛
  TypeError）。补实现后大概率保持 PASS，但同样从未检验真实语义，量大（数百例）
  不逐条列。
- **真通过（噪声，约 94 例）**：测试故意传非函数（`arr.every()`、`comparefn=null`
  等，集中在 `Array/prototype` 的 es5conform 老用例），引擎抛错理由与规范一致，
  不属假阳性。

## 三、Tier A 簇清单（13 簇，128 例）

每簇给出：缺失面（gox 内 `typeof` / `getOwnPropertyDescriptor` 实测）、遮蔽用例数
（TierA，assert.throws 通道）、**敞口**（当前 PASS 且源码引用该 API 的全部用例 ——
补实现后全部进入真检验区）、代表用例。

| # | 簇 | 缺失面（gox 实测） | TierA | 敞口 |
|---|-----|--------------------|------:|-----:|
| C1 | Map/Set/WeakMap upsert | `Map/Set/WeakMap.prototype.getOrInsert·getOrInsertComputed` 全 undefined | 23 | 23 |
| C2 | Iterator helpers 二批 | `Iterator.prototype.{chunks,windows,indexed,toAsync}`、`Iterator.zipKeyed` undefined（take/drop/map/filter/flatMap/reduce/toArray/some/every/find/zip/from 已实现） | 11 | 17 |
| C3 | DataView.prototype 本体 | `DataView.prototype === undefined` —— 全部 `DataView.prototype.*` 都是"reading X on undefined" | 10 | 119 |
| C4 | %GeneratorPrototype% 三方法 | `%GeneratorPrototype%.{next,return,throw}` undefined —— 方法以**实例魔法属性**存在（`object/generator.go` GetProperty 按 receiver 分发），原型上没有；`AsyncGeneratorPrototype` 三方法齐全 | 6 | 27 |
| C5 | Uint8Array base64/hex 残缺 | `setFromHex/setFromBase64/setFromHexInto/setFromBase64Into/toHex/fromHex` undefined（`fromBase64/toBase64` 已实现） | 6 | 12 |
| C6 | ArrayBuffer transfer 族 | `transfer/transferToFixedLength/sliceToImmutable` undefined | 3 | 3 |
| C7 | %TypedArray% 抽象静态 | `TypedArray.from/of` undefined | 8 | 8 |
| C8 | Map/Set.prototype size 访问器 | `getOwnPropertyDescriptor(Map.prototype,'size') === undefined` | 1 | 1 |
| C9 | %ThrowTypeError% 毒丸 | `Function.prototype` 的 `caller/arguments` 访问器缺失：strict 函数读 caller 返回 undefined 而非抛 TypeError（53 例全在 `built-ins/Function/15.3.5.4_2-*gs`，用例里 `gNonStrict.caller.throwTypeError` 靠 undefined 读属性抛 TypeError 过关） | 53 | 83 |
| C10 | RegExp.prototype[Symbol.matchAll] | undefined（`[Symbol.replace]` 已实现） | 3 | 13 |
| C11 | SharedArrayBuffer.prototype | `grow/byteLength` 缺失（SAB 构造器在，方法走 ArrayBuffer 的 receiver 校验） | 1 | 29 |
| C12 | Map/Set IteratorPrototype next | `map[Symbol.iterator]().next === undefined`（for-of/spread 走 VM 原生快路径未暴露，**用户层迭代协议已断**） | 2 | 2 |
| C13 | Date.prototype set 族 | `setFullYear/setMonth/setDate/setHours/setSeconds` undefined（get 族与 setTime/toISOString 在） | 1 | 3 |
| | **合计** | | **128** | **325** |

代表用例（每簇一例）：

- C1 `built-ins/WeakMap/prototype/getOrInsert/does-not-have-weakmapdata-internal-slot-array.js`
  —— `WeakMap.prototype.getOrInsert.call([], {}, 1)`：期望内部槽校验 TypeError，
  实际 "reading 'call' on undefined"。
- C2 `built-ins/Iterator/prototype/chunks/chunkSize-not-a-number.js` ——
  `iterator.chunks()`：实际 "undefined is not a function"。
- C3 `built-ins/DataView/prototype/buffer/invoked-as-accessor.js` ——
  `DataView.prototype.buffer`：实际 "reading 'buffer' on undefined"。
- C4 `built-ins/GeneratorPrototype/return/this-val-not-generator.js` ——
  `GeneratorPrototype.return.call({})`：期望 GeneratorValidate TypeError，实际
  "reading 'call' on undefined"。
- C5 `built-ins/Uint8Array/prototype/setFromHex/detached-buffer.js`。
- C6 `built-ins/ArrayBuffer/prototype/transfer/this-is-sharedarraybuffer.js`。
- C7 `built-ins/TypedArray/from/invoked-as-func.js` —— `var from = TypedArray.from;
  from([])`：实际 "undefined is not a function"。
- C9 `built-ins/Function/15.3.5.4_2-2gs.js`（族内 53 例同机理）。
- C10 `built-ins/RegExp/prototype/Symbol.matchAll/species-constructor-is-not-object-throws.js`。
- C12 `built-ins/MapIteratorPrototype/next/does-not-have-mapiterator-internal-slots-map.js`。

排期含义：**C9（53 例）与 C3（敞口 119）是仅有的两个两位数组**，其余都是小簇。
C1/C2/C4/C5/C7 与既有的簇单（Iterator helpers、Generator 原型化、TypedArray 收尾）
同源，可并入各自的主修复单，不需要单开；C12 的用户层迭代协议断裂值得单独立单
（它不是 test262 假阳性问题，是功能缺陷）。

## 四、与任务书已知簇的口径对照

任务书记载"Generator.return 族 71 例、RegExp 原型族 8 例"（T1-2 时代、35.69%
基线的静态估计）。本轮在 62.14% 基线上实测：

- **Generator 族**：病根未变（%GeneratorPrototype% 三方法仍整体缺失），但该族
  大部分用例当前**直接 FAIL**，不再冒充 PASS；仍 PASS 的 21 条引用里，6 条走假
  阳性通道（C4），其余是 `AsyncGeneratorPrototype`（三方法已实现）的真通过。
- **RegExp 原型族**：本轮确证为 `RegExp.prototype[Symbol.matchAll]` 缺失
  （C10，TierA 3 例 / 敞口 13）；旧估 8 例的其余部分已被此间的修复吸收或转为
  直接 FAIL。

**两簇的"病根"都还在，但"遮羞布"规模已随基线推进收敛到上表数字，以本轮探针
实测为准。**

## 五、遮羞布期台账规则（补簇时的动作模板）

每补一簇，在该簇的落地评论里写三行：

1. **前数字**：本表该簇 TierA / 敞口 + 全量 built-ins 合规率；
2. **后数字**：同口径全量 A/B（逐用例三项 diff 全 0 才算零回归），其中本簇敞口
   内的用例逐条标注 PASS 保持 / FAIL；
3. **归因**：敞口内新出现的 FAIL 一律标"**遮羞布被揭**"（用例首次真正执行规范
   语义），不计入回归；**敞口外**的 FAIL 才是回归，必须回修。

## 六、复现命令

```bash
# 1) 常规基线
gox test262 -root <test262> -suite built-ins -jobs 16 -json /tmp/base.json -quiet

# 2) 探针跑批（分片模型与常规完全一致，结果可逐用例对账）
gox test262 -root <test262> -suite built-ins -jobs 16 -probe -json /tmp/probe.json -quiet

# 3) 归簇：对 probe.json 里 pass=true 且带 fp_hits 的用例，取
#    ^undefined|null is not a (function|constructor)$ 或
#    ^Cannot read properties of (undefined|null) \(reading '([^']+)'\)$ 的 hit，
#    再按用例源码引用 §三 表中的 API 名归簇。
```

## 七、遗留与假设

1. 探针覆盖 `assert.throws` / `assert.throwsAsync` 通道，并已扩展 negative
   frontmatter 通道（`-probe` 时 negative=runtime 且通过的用例会把顶层错误合成
   为 fp_hit）。实测 built-ins 的 192 条 negative 用例**全部是 parse/early，没有
   runtime 负面用例**——该通道在 built-ins 为空（对 language 套件才有意义）。
2. "undefined 信号"判据抓不到"API 存在但内部第 N 层调用链缺件"的深层遮蔽（如
   C10 的 species 路径、`Error.prototype()` 的消息渲染偏差）——它们归入 Tier B，
   未计入 128。
3. 敞口统计基于源码文本引用（可能含注释误引），供排期参考；补簇时的逐用例 A/B
   才是验收口径。
4. `-probe` 的注入只发生在 PASS 且 `-probe` 时写 `fp_hits`；常规跑批（不带
   `-probe`）零影响。

---

## 八、批次战报（2026-10-28 夜批：rFXXbq 引擎批次，×T1-4 协同）

> 环境：test262 checkout 为 main tarball（2026-10-28 下载，tc39/test262），
> built-ins 23822 例 / language 23726 例，分片 128、`-jobs 16`。§六 的
> `-probe` 清单已由并行会话入库（a1576f669 / 4cb502fff），本节补充**引擎修复
> 批次**的 A/B 数字与遮羞布面的全量口径。

### 8.1 合规率前后（逐用例 A/B，only_a/only_b = 0）

| 套件 | 前 | 后 | 增量 |
|------|-----|-----|------|
| built-ins | 14802/23822 = 62.1358% | **15055/23822 = 63.1979%** | **+253 例 / +1.06pp，退步 0** |
| language | 20309/23726 = 85.5981% | 20309/23726 = 85.5981% | ±0（parser 修复对该套件零翻转） |

单簇：`built-ins/String/prototype` **502/1073 (46.78%) → 673/1073 (62.72%)**（+171）。

### 8.2 本批四项引擎修复（全为零回归）

1. **class 成员名接受保留字**（parser/parser.go）：`return(){}` / `get if()` /
   `static while()` 此前整族 parser 报错 —— 对象字面量路径早有
   `isKeywordProperty`，class 体没对齐。Iterator/prototype 一族由此开门
   （该族剩余失败是 `Iterator` 基类未实现，是另一张单）。
2. **String.prototype 泛型方法 this 强制转换**（stdlib/string_methods.go 等）：
   28 个方法从 `this.(*object.String)` 收口改为
   `thisStringCoerced = RequireObjectCoercible + ToString`（规范口径）；   
   toString/valueOf/toLocaleString 保持 ThisStringValue 不动。receiver 类失败
   从 417 降至 <20。
3. **转换点按规范补 ToPrimitive(string 提示)**（`toPrimitiveToString` /
   `ordinaryToPrimitiveString`）：@@toPrimitive → toString → valueOf，
   结果必须是原始值否则 TypeError —— 修掉 `split`/`replace`/`replaceAll` 的
   searchValue/separator/replaceValue 静默字符串化（S15.5.4.11_A1_T15/T16 一族），
   以及 `ToString(Symbol)` 必须抛 TypeError（*-as-symbol / exception-symbol 一族）。
   **回调桥错误必须立即传播**：桥出错返回 `UndefinedSingleton` 而非 nil，
   不检查 `callbackThrown()` 会吞用户异常并拿 "undefined" 继续跑 —— 实测
   `repeat/return-abrupt-from-this` 一例 panic 且污染同分片后续用例（A6 假失败）。
4. **Number.prototype 五方法解包 PrimitiveObject**（stdlib/number_methods.go）：
   `new Number(x).toString(33)` 此前被拒（thisNumberValue 语义）；顺带修
   `"".repeat()` 无参 `args[0]` 越界 panic（潜伏 bug，本轮被 repeat 用例踩中）。

### 8.3 T1-4：已知两族现状与遮羞布面全量口径

- **已知两族现状**：`Generator.prototype.return` 族（旧台账 71 例）**已全部转真
  通过**（当前 0 失败）；`RegExp` 一等对象族已被正确揭盖（RegExp/prototype 相关
  现在是 142 条**真失败**，不再是假通过）。
- **遮羞布面全量存量：1957 例**（通过、且源码含 `assert.throws(TypeError`，补齐
  校验即翻转）。Top：TypedArray/prototype 355、Array/prototype 237、
  Temporal/ZonedDateTime 115、Object/defineProperty 102、String/prototype 99、
  Date/prototype 82 —— 是 §六 -probe 128 例清单之外的全量上界。
- **反向信号**："期望抛错但未抛"的失败 1251 例 —— 与遮羞布面同源：引擎尚无通用
  ToPrimitive(valueOf) 协议、属性描述符校验不全；这些位置补齐后上面的 1957 例会
  成批 PASS→FAIL（遮羞布被揭，**不是回归**，按本节口径标注）。
- **本轮 A/B 的 32 例 PASS→FAIL 全部当轮处理**：20 例 Symbol 接收者按规范修真
  （ToString(Symbol) 抛 TypeError），12 例 ToPrimitive 缺口修真或经排查消除，
  最终退步归零 —— 未采用任何"标注后放过"的口径。

### 8.4 遗留（下一批入口，按当前失败面 Top）

String/prototype 400、RegExp/property-escapes 447（\p{...} 属性表）、
Iterator 基类 410、Object/create 295、DataView/prototype 366（访问器形态 +
ToNumber 抛错顺序）。
