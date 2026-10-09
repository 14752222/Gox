# 移动端真机性能基线：方法与空表（rpr9zf）

> **状态：基线数字「待采集」，本文档先给方法与空表。**
> 本机没有 adb、没有 Xcode/macOS、缺鸿蒙签名材料（看板单 rpr9zf 暂停原因），
> 因此本文**一个数字都没有** —— 表里的空格不是忘了填，是**还没采**。首次真机
> 验收后按 [§5](#5-回填流程) 回填，并注明机型/系统/日期/采集人。
> 严禁拿开发机 bench 或模拟器数字填真机的表。

本文只管**真机**指标。与另两篇文档的分工：

| 文档 | 管什么 | 机器 |
|---|---|---|
| `docs/performance.md` | 解释器算力（fib28/timers10k）、UI 帧间隔与 **GC 毛刺** | **开发机**（桌面） |
| 本文 | 屏幕密度、触控与多指手势、帧率、内存、冷启动 | **真机**（Android/iOS/鸿蒙设备） |
| `docs/mobile-regression-checklist.md` | 功能回归清单（§3 逐条打勾）+ 本文数据的采集时机 | 真机 |

两篇的**数字不可直接相减**：口径、单位、负载形态都不同（见 [§4](#4-与开发机-bench-的对比口径)）。

采集统一走一条命令（Android）：

```bash
bash scripts/mobile-device-accept.sh --package <appId>
#   无设备时先看它会跑什么：bash scripts/mobile-device-accept.sh --dry-run
```

产物落在 `bench-results/device-accept-<yyyy-mm-dd>-<device>.json`（含设备型号 /
ABI / Android 版本 / 采集时间戳 / 每项原始命令与输出），终端另打印人读摘要。
脚本采不到的项写 `null` + `status: unavailable`，并汇总成「本次未完成项」——
**不造假数据**是它的硬约束。

---

## 1. 屏幕与密度（§1）

**为什么单列**：断点（compact/medium/expanded）、安全区、触控目标尺寸判定的都是
**逻辑 dp**，而设备给的是物理 px。px→dp 的换算错了，后面所有布局断言都白验。

| 指标 | 单位 | 采集命令 | M1 (iOS) | M2 (Android) | M-F1 | M-F2 |
|---|---|---|---|---|---|---|
| 物理分辨率 | px（`WxH`） | `adb shell wm size` | _待采集_ | _待采集_ | _待采集_ | _待采集_ |
| 屏幕密度 | dpi | `adb shell wm density` | _待采集_ | _待采集_ | _待采集_ | _待采集_ |
| 逻辑尺寸 | dp | `px * 160 / dpi`（脚本换算） | _待采集_ | _待采集_ | _待采集_ | _待采集_ |
| 刷新率 | Hz | `adb shell dumpsys display \| grep -m1 -i refreshrate` | _待采集_ | _待采集_ | _待采集_ | _待采集_ |

判定口径：

- 换算公式 `dp = px * 160 / dpi` 与 `gfx/mobile` 的 `Display.Scale` 一致；
  脚本输出的 `logical_dp` 就是断点判定用的值，**不是**物理 px。
- 内外屏密度不同（如 2x/3x）的折叠机，内外屏**各采一次**（折展前后分别跑脚本）。
- 刷新率是 §3 帧率的上限：120Hz 机器上 60fps 就是掉了一半帧，别只看绝对 fps。

> iOS / 鸿蒙：本脚本只走 adb。iOS 用 `xcrun simctl` / Instruments、鸿蒙用 `hdc`
> 的等价命令，**采集方法待补**（等硬件到位后另开小节，先不猜测命令）。

---

## 2. 触摸与多指手势（§2）

**为什么单列**：Gox 的软光栅链路里，输入从 Java 侧进来再过 JNI，多指（pinch/
双指缩放）在 v1 无组件级手势（见 `mobile-regression-checklist.md` §6 边界表），
但**宿主有没有把多指事件送到引擎**必须靠证据说话。

| 指标 | 单位 | 采集命令 | M1 | M2 | M-F1 | M-F2 |
|---|---|---|---|---|---|---|
| 单指 tap 注入 | rc（0=成功） | `adb shell input tap <x> <y>` | _待采集_ | _待采集_ | _待采集_ | _待采集_ |
| 单指 swipe 注入 | rc（0=成功） | `adb shell input swipe x1 y1 x2 y2 400` | _待采集_ | _待采集_ | _待采集_ | _待采集_ |
| 双指张开/捏合注入 | rc（0=成功） | `adb shell "input motionevent DOWN …; POINTER_DOWN …; MOVE …; POINTER_UP …; UP …"` | _待采集_ | _待采集_ | _待采集_ | _待采集_ |
| 内核事件证据 | 命中次数 | `adb shell "timeout 5 getevent -l -c 200"` | _待采集_ | _待采集_ | _待采集_ | _待采集_ |

判定口径：

- **多指的唯一硬证据是 `ABS_MT_SLOT` / `ABS_MT_TRACKING_ID`**：`getevent` 样本里
  这两个字段的命中数 ≥ 2，才说明设备真的上报了多指；只有 1 个（或 0）就是
  **单指**，脚本不会把「注入命令返回 0」当成「多指已验证」。
- `input motionevent` 需要 **Android 12+**；老设备会报未知子命令，脚本如实标
  `unavailable`，此时该项**只能人工验**（手指真捏一次），不能留空当通过。
- iOS / 鸿蒙的多指注入没有 adb 等价物，方法与数据均 _待采集_。

---

## 3. 帧率、内存与冷启动（§3）

| 指标 | 单位 | 采集命令 | M1 | M2 | M-F1 | M-F2 |
|---|---|---|---|---|---|---|
| 冷启动 `TotalTime` | ms | `adb shell am force-stop <pkg>` → `adb shell am start -W -n <pkg>/<act>` | _待采集_ | _待采集_ | _待采集_ | _待采集_ |
| 渲染帧数（采样窗口） | 帧 | `adb shell dumpsys gfxinfo <pkg> reset` → 注入滑动 → `dumpsys gfxinfo <pkg>` | _待采集_ | _待采集_ | _待采集_ | _待采集_ |
| 掉帧数（Janky frames） | 帧 | 同上 | _待采集_ | _待采集_ | _待采集_ | _待采集_ |
| 帧耗时 p50 / p95 / p99 | ms | 同上（gfxinfo 的分位数字段） | _待采集_ | _待采集_ | _待采集_ | _待采集_ |
| 内存 PSS | kB | `adb shell dumpsys meminfo <pkg>` | _待采集_ | _待采集_ | _待采集_ | _待采集_ |

判定口径（都会写进 JSON 的 `note`，避免后人误读）：

- **冷启动**：`am start -W` 的 `TotalTime`（ms，含进程启动 + 首帧）。取 **3 次中位数**，
  每次前先 `am force-stop` —— 不 force-stop 测的是热启动，两个数差一个量级。
- **帧率**：脚本给的 `fps_estimate` 是「渲染帧数 / 采样窗口（默认 5s）」的**粗估**，
  不是逐帧直方图；要精细结论看 gfxinfo 的 p95/p99 分位数。分位数缺失说明该
  Android 版本没输出，**不要**拿 p50 乘个系数补上。
- **内存**：PSS 取 `dumpsys meminfo` 的 `TOTAL` 行首个数字，单位 **kB**；
  对比开发机 bench 的 `rss_kb` 时口径不同（见 §4）。
- 采样窗口内脚本持续注入滑动制造负载；**静置不动测出来的帧率没有意义**。

---

## 4. 与开发机 bench 的对比口径

真机数字和 `docs/performance.md`（开发机）的数字**不能直接相减**，理由三条：

| 维度 | 开发机 bench（performance.md） | 真机（本文） |
|---|---|---|
| 指标 | 解释器算力、GC 停顿、帧间隔 | 密度、触控、帧数/掉帧、PSS、冷启动 |
| 内存 | `rss_kb`（进程常驻，含 V8/Go 堆基线） | `pss_kb`（按页分摊，Android 口径） |
| 负载 | `scripts/bench/*.js` 合成脚本 | 真实 App + `input` 注入的真实交互 |

能做的有意义对比只有两类，**跨设备纵向**与**同设备横向**：

1. **同机型跨版本**：同一台 M2 机上，本次 release 的冷启动 / PSS / 掉帧数 vs
   上一轮存档 —— 这才是回归信号（涨了就是回退）。
2. **跨机型横向**：M1 与 M2 之间**只比趋势**（如「冷启动都 < 1s」），不比绝对值
   —— 硬件与系统调度不同，绝对值没有可比性。

阈值建议（等首批数据回来再定，先不写死）：冷启动、PSS、掉帧率各取首轮中位数
作为基线，回归时**相对基线**设 ±__% 阈值，填在本表下方。

| 指标 | 首轮基线（_待采集_） | 回归阈值（_待定_） |
|---|---|---|
| 冷启动 TotalTime | _待采集_ | _待定_ |
| 内存 PSS | _待采集_ | _待定_ |
| 掉帧率（janky/total） | _待采集_ | _待定_ |

---

## 5. 回填流程

1. 插上设备（**一次只插一台**），确认 `adb devices` 显示 `<serial>\tdevice`。
2. `bash scripts/mobile-device-accept.sh --package <appId>` —— §1~§3 一次取完。
3. 看终端摘要；有「本次未完成项」就按提示手工补采，并在本文对应格子里标
   **（手工补采）** 与原因。
4. JSON 存档随仓库提交（命名 `bench-results/device-accept-<日期>-<机型>.json`），
   把本文 §1~§3 的表按机型列填好，**写清机型/系统版本/采集日期**。
5. 首轮采完后再定 §4 的回归阈值 —— 没有基线就定阈值等于拍脑袋。

> 脚本退出码：0 = 全采到；1 = 有未完成项（JSON 仍落盘，缺项 `null`）；
> 2 = adb 不在 PATH；3 = 设备不可用（无设备 / 多台未指定 / 未授权）。

---

## 6. 已知采集限制

| 限制 | 说明 |
|---|---|
| 只覆盖 Android | iOS（Xcode/Instruments）与鸿蒙（hdc + 签名材料）**方法与数据均待补**，等硬件到位 |
| 多指注入要 Android 12+ | 老设备 `input motionevent` 不可用，只能人工验 |
| `getevent` 可能无权限 | 部分厂商禁 `/dev/input`；脚本标 `unavailable`，不伪造证据 |
| 帧率是粗估 | 需要逐帧结论时用 `dumpsys gfxinfo <pkg> framestats` 另采 |
| 模拟器数据不能当基线 | 非 arm64 ABI 时脚本会警告；x86_64 模拟器数据只能验证「能不能跑」 |
