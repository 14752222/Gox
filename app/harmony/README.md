# Gox on HarmonyOS —— 最小宿主壳

这是 Gox 在鸿蒙（HarmonyOS / ArkTS）上的**壳工程**。引擎（lexer → parser → compiler →
VM → stdlib → gfx）一行都不在这里 —— 这里只做两件事：

1. 把 Go 侧编成 `libgox.so`（`-buildmode=c-shared`）装进 HAP；
2. 用一个最小的 `@Entry` 页面（`Image(PixelMap)` + `onAreaChange` + `onTouch`）把它驱动起来。

```
┌── app/harmony (本目录, ArkTS) ──┐   PixelMap 上屏 / 触摸 / 安全区 / 折叠上报
│                                 │
├── gfx/harmony/libgox            │   NAPI: GoxModuleRegister / GoxDispatch + 装配 (package main)
├── gfx/harmony                   │   NAPI 通道: ArrayBuffer 直写 / hilog / 宿主回调
├── gfx/mobile                    │   纯 Go 内核: 事件队列 / 触摸映射 / 泵唤醒 / density·折叠上报
└── gfx (内核, 零改动)            │   node / layout / raster / font / hittest
```

界面结构**一条都不在宿主里**（与 Android 的 `MainActivity` 同一条原则）：Gox 的 UI 全在
脚本里（gfx 的 JSX / 组件），宿主页面只是一块画布。要看的界面脚本是
`entry/src/main/resources/rawfile/app.js`。

> ⚠️ **还没装到模拟器跑过**。已验证到「命令行 hvigor 构建出 HAP + 产物内容核对」这一层
> （见文末「已验证到什么程度」），但**触摸、上屏还没在设备上实跑过**。
> **验收口径（2026-10-02 拍板）：模拟器即可，不依赖真机折叠屏** —— 折叠上报（HF2）的
> `foldStatusChange` 模拟器不触发，其逻辑已由桌面资产测试覆盖（真 VM 跑 `app.js`）；
> 模拟器要验的是 HF1：模块加载 / 上屏 / 触摸 / 安全区。

## 前置条件

| 需要 | 版本 | 本机实测位置 |
| --- | --- | --- |
| DevEco Studio | 与本机一致即可 | `H:/DevEco Studio` |
| HarmonyOS SDK (Native) | `apiVersion 26` / Platform 26.0.0 | `H:/DevEco Studio/sdk/default/openharmony/native` |
| OHOS clang | 15.0.4（SDK 自带） | 同上 `llvm/bin/clang.exe` |
| `hdc` | SDK 自带 | `H:/DevEco Studio/sdk/default/openharmony/toolchains/hdc.exe` |

**SDK 版本不一致时**：改工程根的 `build-profile.json5` 里的 `compatibleSdkVersion`
（对着 `<sdk>/native/oh-uni-package.json` 的 `apiVersion` 填），DevEco 打开工程时也会提示。

## 构建（四步）

```bash
# 1) 编 Go 侧 —— 唯一支持 cgo 的那条路, 产物带 ELF 目标校验
bash scripts/build-harmony.sh --abi arm64        # 真机
bash scripts/build-harmony.sh --abi x86_64       # 模拟器 (x86_64 主机上原生执行)
#    找不到 SDK 时: bash scripts/build-harmony.sh --sdk "H:/DevEco Studio/sdk/default/openharmony/native"
#    产物: dist/harmony/<abi>/libgox.so
#
#    ⚠️ 鸿蒙**没有** GOOS=openharmony —— 脚本走 GOOS=linux + OHOS clang + OHOS sysroot
#       (musl) 产出 AArch64 ELF。手工照抄 GOOS/GOARCH 很容易"编成功但链错目标"。

# 2) 拷进壳工程 (entry/libs/ 下的 .so 不入库, 每次重建都要重新拷)
cp dist/harmony/arm64/libgox.so app/harmony/entry/libs/arm64-v8a/

# 3) 构建 HAP —— 已验证的命令行姿势 (不依赖 wrapper, hvigor 版本随 DevEco 走):
export DEVECO_SDK_HOME="H:/DevEco Studio/sdk"
export NODE_HOME="H:/DevEco Studio/tools/node"
#    先装一次依赖 (oh-package.json5 里那个 file: 指向的类型包要它来链接):
node "H:/DevEco Studio/tools/ohpm/bin/pm-cli.js" install --all
#    再构建:
node "H:/DevEco Studio/tools/hvigor/bin/hvigorw.js" --mode module \
     -p module=entry@default -p product=default -p buildMode=debug \
     assembleHap --no-daemon
#    产物: entry/build/default/outputs/default/entry-default-unsigned.hap
#    未签名 —— 装机要在 DevEco 里 File → Project Structure → Signing Configs 勾自动签名

# 4) 装到设备并看日志
"$SDK/toolchains/hdc.exe" install -r entry/build/default/outputs/default/entry-default-signed.hap
"$SDK/toolchains/hdc.exe" shell hilog -T Gox
```

**日志为什么专门看 `Gox` 这个 tag**：与应用进程一样，鸿蒙的 stdout/stderr 在设备上
也看不到 —— Go 侧所有"跳过了、失败了"的分支都走 `gfx/harmony.Logf`（内部
`OH_LOG_Print(..., "Gox", "%{public}s", ...)`）。"黑屏 + 无日志"是没法查的，这条就是为它准备的。

## NAPI 契约（改一处必须同步三处）

Android 的 JNI 靠**符号名**（`Java_com_gox_GoxRuntime_nativeTick`）由 JVM 自动绑定；
鸿蒙的 NAPI **没有这一层** —— 库必须自己注册模块，方法靠**序号**分发。所以：

| 位置 | 内容 |
| --- | --- |
| `gfx/harmony/libgox/main.go` | `goxMethods` 方法表（**顺序 = 序号**）+ `//export GoxModuleRegister` / `//export GoxDispatch` |
| `gfx/harmony/libgox/bridge.c` | `napi_trampoline`（把回调摊平成 `void*`）+ 模块注册 `napi_module_register` |
| `entry/src/main/cpp/types/libgox/index.d.ts` | 同一个导出面的类型声明（ArkTS 侧靠**名字**调用） |

三处对不上时的症状都不是编译错：

- **少了模块注册** → `import nativeGox from 'libgox.so'` 拿到 `null`，hvigor 构建照样绿；
- **名字写错** → ArkTS 拿到 `undefined`，调用即 `not a function`；
- **序号错位**（表里中间插了一行）→ 调 `tick` 却跑了 `resize`，极难归因。

`gfx/mobile/fold_contract_test.go` 的鸿蒙用例把这三条静态钉住（含 ArkTS 侧**实际调用**的
每个名字 vs `goxMethods` 的双向核对）。

### 三条 cgo 结构约束（本工程在别处也踩过四次）

1. **含 `//export` 的文件，cgo preamble 里不能有定义** —— preamble 会被复制进两个生成的
   C 文件，放定义就是链接期 `duplicate symbol`。所以全部函数定义与模块注册都在 `bridge.c`。
2. **preamble 必须纯 ASCII** —— clang 会直接编译这段文本，中文标点报 `unexpected character`。
   所以里面只用 C 行注释（`//`），中文说明一律放 `bridge.c`。
3. **preamble 里不能出现嵌套的块注释标记** —— cgo 会剥掉外层标记，嵌套会让它提前闭合，
   症状是 `C source files not allowed when not using cgo`（看着像 cgo 被禁用，其实是注释断了）。

另外：**`-tags harmony` 不能省**。`//go:build harmony` 的文件在 `GOOS=linux` 下不会被选中，
漏了这个 tag 的报错是误导性的 `build constraints exclude all Go files`。

### 工程配置的三处"缺一个就起不来"（第一次构建全撞上）

| 缺的东西 | 报错 |
| --- | --- |
| `hvigor/hvigor-config.json5` | `00304004 Not Found: Hvigor config file … does not exist` |
| `AppScope/app.json5`（含它引用的 `$media:app_icon` / `$string:app_name`） | `app.json5 file not found. At file: …\AppScope\app.json5` |
| `compatibleSdkVersion` 写成**数字** | `Schema validate failed … must be string` —— 必须写成 `"26.0.0"` 这种字符串 |

另有一条 ArkTS 语言层面的坑：**实例方法必须 `this.` 调用**。把全局 `vp2px(...)` 包成本地
`toPx(...)` 之后，调用点写 `toPx(...)` 会报
`Cannot find name 'toPx'. Did you mean the instance member 'this.toPx'?` —— 全局函数是自由函数，
类方法不是，调用点一个都不能省 `this.`。

顺带：全局 `vp2px` / `getContext(this)` 都已在 API 18 弃用（`@useinstead` 指向
`ohos.arkui.UIContext.UIContext`），本工程已换成 `this.getUIContext().vp2px(...)` 与
`this.getUIContext().getHostContext()`。注意后者的签名是 **`Context | undefined`**，必须显式判。

## 折叠屏（HF2）

**链路**（与 Android / iOS 三端同构，最后都进同一个纯 Go 入口）：

```
display.isFoldable() / display.on('foldStatusChange') / display.getCurrentFoldCreaseRegion()
window.on('windowSizeChange')
        ↓  拼 JSON
libgox.setDisplayFold(json)                     ← NAPI 导出 (方法序号 18)
        ↓  GoxDispatch → gfx/mobile.ReportDisplayFold  (解析/校验/归一化, **可单测**)
gfx.ReportPostureFromFold + ReportViewport
        ↓
脚本侧 gx/viewport 的 reservedRegions() / hasFold() / layoutMode() / widthClass()
```

**为什么逻辑在 Go 侧**：harmony tag 下的代码在桌面开发机上**编译不到**（只有交叉编译才看得见
错误），测试面为零。所以宿主只做"把系统给的如实报上去"（`FoldStatus` 如实翻成
flat/half-open/folded、`creaseRects` 的第一条按宽高比判 vertical/horizontal），姿态归一化、
结构判据、保留区裁剪全在 `gfx/mobile/fold.go`。

**判据映射**（`entry/src/main/ets/native/GoxDisplayFold.ets`）：

| 鸿蒙 API | 我们的映射 |
| --- | --- |
| `FoldStatus.FOLD_STATUS_EXPANDED` | `posture = "flat"` |
| `FoldStatus.FOLD_STATUS_HALF_FOLDED` | `posture = "half-open"` |
| `FoldStatus.FOLD_STATUS_FOLDED` | `posture = "folded"` |
| `getCurrentFoldCreaseRegion().creaseRects[0]`，**宽 < 高** | `orientation = "vertical"`（折痕是竖带 ⇒ 切左右） |
| 同上，**宽 >= 高** | `orientation = "horizontal"`（横带 ⇒ 桌面模式） |
| 折痕矩形 | `kind = "division"`（鸿蒙只给折痕这一种，没有 Android 那种 occlusion） |

**三条硬约束**（违反的表现全是"静默失效"，不是崩溃）：

1. **折痕不随 flat 清除**：`getCurrentFoldCreaseRegion()` 只在半折叠态返回非空。平展时
   若把 hinge 丢掉，"折起来 → 展开 → 再折起来"会让分栏比例跳变。所以宿主**缓存**折痕，
   非半折叠态仍原样上报、只把 `active` 置 false。
2. **首次上报替换整张显示器表**：所以 `start()` 里等 `getLastWindow()` 拿到之后立刻补一次
   **带全尺寸**的上报 —— 否则"窗口所在显示器"解析不到，折叠双栏静默失效。
3. **姿态只做特性信号，绝不驱动布局**（华为官方《多设备适配屏幕差异》原文："不推荐使用
   折叠状态监听接口实现页面响应式布局和接续"）。理由：上下折展开后宽度仍 < 600vp，
   用 `foldStatus` 推布局必然给出错误的"大屏"判断；且官方时序里 `windowSizeChange`
   **早于** `foldStatusChange`，用姿态驱动会慢一帧。所以**布局响应式一律走尺寸**
   （`windowSizeChange` → 窗口像素 + `densityPixels` → dp 断点）。
   断点必须与内核 `gfx/viewport.go` 的 `deriveSizeClasses` 同一组：**宽 600 / 840dp 三档，
   高 480dp 两档**。

## 验收清单（HF1 / HF2）

HF1（通道）：

| # | 断言 |
| --- | --- |
| 1 | 有启动日志 `libgox 模块已注册 (19 个导出)`（`hdc shell hilog -T Gox`） |
| 2 | 不是黑屏：界面上有 `Gox on HarmonyOS` / 后端名 / 一行计数 / 一个按钮 |
| 3 | 点按钮 → 计数递增（触摸链路通） |
| 4 | 顶部/底部内容不被状态栏、刘海、手势条压住（`setInsets` 生效） |
| 5 | 点输入框弹软键盘（**IME 提交链路要 M2 才真接**，现在只有 `imeShow` 日志） |
| 6 | 旋转/改窗口尺寸后界面按新尺寸重排，且**会话不重建**（计数不归零） |

HF2（折叠）：

| # | 姿态 | 断言 |
| --- | --- | --- |
| 1 | 直板机 / 模拟器 | 显示"未检测到折痕"，`布局建议: single` |
| 2 | 半折 | `姿态: half-open`、`布局建议: dual`、`折痕 1 条` |
| 3 | 展开（flat） | 折痕**仍在**（`hasFold()` 恒 true，列数不跳），`布局建议` 按宽度档走 |
| 4 | 折↔展来回切 | 分栏比例不跳、界面不闪 |

## 首帧自检清单

真机上第一次跑，按这个顺序看（前一条不过就别看后面的）：

1. **库加载了** → 日志里有 `libgox 模块已注册 (19 个导出)`。没有这行说明 NAPI 模块没注册
   或 `.so` 没进 `libs/<abi>/`（先确认 abi 目录名是 `arm64-v8a`，不是 `aarch64-linux-ohos`）。
2. **会话起来了** → 日志里有 `帧缓冲: WxH density=...`。没有则 `nativeGox.init` 就没走到。
3. **不是黑屏** → 见 HF1 第 2 条。
4. **颜色对** → 帧缓冲是 `image.RGBA`（内存字节序 R,G,B,A），PixelMap 用
   `PixelMapFormat.RGBA_8888` + `writeBufferToPixels`（纯 memcpy），**不需要换通道**。
   真出现红蓝互换，先查这里。
5. **点得动** → 见 HF1 第 3 条。
6. **点不动先查坐标单位** → ArkUI 的 `TouchEvent.touches[i].x/y` 与 `Area.width/height`
   都是 **vp**，而内核的 `Surface` 一律按**像素**。漏了 `vp2px()` 的症状是"点左边却响应
   右边"，偏移量与 dpi 成正比，很容易被误判成布局问题。

## 桌面能守住的部分（先跑它，再上真机）

```bash
go test ./gfx/mobile/     # 纯 Go, 开发机直接跑
```

- 触摸映射、泵唤醒、resize 通知、density/折叠上报都在这里；
- `TestHarmonyAssetScriptRunsAndTracksFold` 直接读**本目录的
  `entry/src/main/resources/rawfile/app.js`** —— 那份要打进 HAP 的脚本被改坏（JSX 写错、
  用了相对 import、引用了不存在的模块）时当场红。它还顺带验收 HF2 的**订阅**这一层：
  折叠上报进来后界面读数必须跟着变（只验 Go 侧屏表是验不到订阅断掉的）。

  ⚠️ 写响应式读数时注意一个**实测踩到的坑**：`hasFold()` / `posture()` 是**纯读数**
  （不带订阅），只有 `useXxx()` / `regions()` 那一族才订阅。把纯读数写在三元条件里
  **短路掉**了订阅调用（`hasFold() ? ... regions() ... : "无"`），首帧走 false 分支 ⇒
  这个 effect 一个依赖都没有，之后折起来**永远不会重跑**，且没有任何警告。
  正确写法是先无条件取一次订阅型读数，再决定显示什么（见 `app.js` 里 `foldSummary` 的注释）。

## 已验证到什么程度（2026-10-01）

| 项 | 结果 |
| --- | --- |
| `bash scripts/build-harmony.sh --abi arm64` | ✅ AArch64 共享库（ELF 目标校验通过，28MB） |
| `llvm-readelf -d` | ✅ `NEEDED` = `libace_napi.z.so` / `libhilog_ndk.z.so` / `libc.so` |
| `llvm-nm -D` | ✅ `GoxDispatch` / `GoxModuleRegister` 已导出（`gox_module_init` 已编入） |
| `CGO_ENABLED=0 go build ./...` | ✅ 内核零改动（harmony 代码被 build tag 隔离） |
| 静态契约 + 脚本端到端 | ✅ `gfx/mobile` 鸿蒙用例 6 项全通 |
| **命令行 hvigor 构建（assembleHap）** | ✅ BUILD SUCCESSFUL，ArkTS 编译零告警（仅剩"未配签名"一条 WARN） |
| **HAP 产物内容** | ✅ `libs/arm64-v8a/libgox.so` + `resources/rawfile/app.js` 都在包里 |
| **DevEco 模拟器（验收口径）** | ⬜ **未装**（HAP 未签名；建模拟器实例 + 自动签名都要在 DevEco GUI 里配华为账号，命令行替不了） |

## v1 已知边界（都不是 bug，是没做）

- **单缓冲**：Go 写入与宿主 `writeBufferToPixels` 可能重叠一帧（撕裂）。彻底解决要双缓冲 +
  交换指针，等真机实测出撕裂概率再定。（**换缓冲本身**的竞态已处理：`GoxBridge.resize`
  先重新分配 `ArrayBuffer` 与 PixelMap、再 `bindFrameBuffer` 换地址。）
- **不做脏区上传**：`flush(rects)` 的 rects 被忽略，整块 `writeBufferToPixels`。契约明确
  允许一次额外拷贝（与 Android 一致）；要按 rects 局部刷新的前提是先有真机帧耗时数据。
- **软键盘（IME）未真接**：`imeShow` 只有日志，`imeCommit` 通道已在（19 个导出之一），
  但没有 ArkTS 侧的系统输入法桥 —— 属 M2。
- **六模块 NativeHost 仍是 stub**：`GoxNativeHost.ets` 对 device / app / geo / media /
  permission 全部显式 unsupported（与 Android/iOS 的差距见 `app/NATIVE-HOST.md` 的状态表）。
  已接通的只有 `insets` 与 `displayFold` 两条**纯上报**通道。
- **单指触摸**：多指手势不识别，第二根手指按下即作废整个手势（与 Android 侧一致）。
- **density 只上报不换算**：`pixelRatio` 有了，但 layout 的逻辑像素换算还没做 ——
  所以现在 `font={20}` 就是 20 个物理像素，在高密度屏上偏小（M1 剩余部分）。
- **窗口上屏走 ArrayBuffer + PixelMap**，不是 XComponent 的 native window。后者是后续优化，
  需要壳工程带 cpp 目录并在 native 侧接管 `OnSurfaceCreated`；v1 不做 —— 存一个不消费的
  `OHNativeWindow*` 只会变成悬垂指针隐患。
