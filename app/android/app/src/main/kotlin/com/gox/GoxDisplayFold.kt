package com.gox

import android.app.Activity
import android.content.res.Configuration
import android.util.Log
import androidx.core.util.Consumer
import androidx.window.java.layout.WindowInfoTrackerCallbackAdapter
import androidx.window.layout.FoldingFeature
import androidx.window.layout.WindowInfoTracker
import androidx.window.layout.WindowLayoutInfo
import org.json.JSONArray
import org.json.JSONObject
import java.util.concurrent.Executor

/**
 * 折叠屏上报: androidx.window 的 [FoldingFeature] → JSON → `nativeSetDisplayFold`。
 *
 * 契约的另一端是 `gfx/mobile.ReportDisplayFold` (解析 + 校验 + 归一化, **可单测**)
 * 与 `gfx/android/libgox/main.go` 的 `//export Java_com_gox_GoxRuntime_nativeSetDisplayFold`。
 *
 * ── 三条纪律 (与内核注释同源) ──────────────────────────────────────────
 *
 * ① **坐标一律是设备像素**: FoldingFeature.bounds 已经是 View 坐标系下的
 *    **像素** (不是 dp) —— 与内核的口径天然一致, 不用换算。反过来说,
 *    截图工具里那套 "宽 1024dp @2x" 的推算在这里**不要**用。
 * ② **不猜**: 宿主只报"看到的"。姿态判定 (half-open / flat / folded) 与
 *    折痕归一化全在 `gfx/mobile/fold.go`, 那边有 `go test ./gfx/mobile` 的
 *    测试面; 这里的代码在桌面开发机上**编译不到**, 写错了定位成本极高。
 * ③ **不自己算 hasFold**: 结构性判据在 `gfx/mobile.StructuralRegions`。
 *    这里只把 `isSeparating` 如实翻成 kind (division / occlusion)。
 *
 * ── 判据 (取自 Android 官方文档) ───────────────────────────────────────
 *
 * | FoldingFeature 属性 | 我们的映射 |
 * |---|---|
 * | `state == HALF_OPENED` | `posture = "half-open"` (设备真的折着) |
 * | `state == FLAT` | `posture = "flat"` (完全展开) |
 * | `isSeparating == true` | `kind = "division"` (铰链分隔出两个逻辑区域) |
 * | `isSeparating == false` | `kind = "occlusion"` (只遮住一条, 不分割) |
 * | `orientation == VERTICAL` | 折痕是**竖带** (左右分屏), 对应内核的 "vertical" |
 * | `orientation == HORIZONTAL` | 折痕是**横带** (上下/桌面模式) |
 *
 * ⚠️ **表述陷阱**: `FoldingFeature.Orientation.VERTICAL` 指的是**折痕线是竖的**
 * (它把窗口切成左右两半), 而不是"屏幕竖着"。这个映射很容易左右/上下写反,
 * 而写反的症状是"避让带跑到另一个轴上"—— 布局看着像随机错位。内核侧的
 * `DisplayHinge.Orientation` 用的是同一个语义 (见 gfx/screen.go), 两端一致。
 */
class GoxDisplayFold(private val activity: Activity) {

    /** 回调线程: 主线程直投 —— 我们只做字段读取与 JSON 拼装, 不做 IO。 */
    private val executor = Executor { it.run() }

    private var adapter: WindowInfoTrackerCallbackAdapter? = null

    /**
     * 已注册的回调。`removeWindowLayoutInfoListener` 要的正是**注册时那个
     * Consumer 实例** (androidx.core.util.Consumer, 不是 Executor) —— 所以
     * 必须自己存着; 想当然地传 activity 或 executor 都会被编译器拦下
     * (实测两轮都撞过)。
     */
    private var listener: Consumer<WindowLayoutInfo>? = null

    /**
     * 最近一次窗口布局信息。onConfigurationChanged 里要靠它复报一次
     * (配置变更不一定伴随新的 Flow 推送, 而尺寸类可能已经变了)。
     */
    @Volatile
    private var last: WindowLayoutInfo? = null

    /**
     * 开始监听窗口布局信息。**幂等** (重复调不会重复注册)。非阻塞。
     *
     * 为什么用 `WindowInfoTrackerCallbackAdapter` 而不是直接 collect 那条
     * `Flow`: `windowLayoutInfo(Activity)` 返回的 Flow **只能在协程里收集**
     * (`collect` 是 suspend), 而这个壳工程不引 kotlinx-coroutines ——
     * 为一个回调拉进整套协程运行时不值。`window-java` 工件的
     * `WindowInfoTrackerCallbackAdapter` 正是为此存在。
     *
     * 实测记 (2026-10-01, 三轮编译才过):
     *   ① `windowLayoutInfo(activity, executor)` → 该重载不存在 (Too many arguments)。
     *   ② 换 CallbackAdapter 后 → "Cannot access class androidx.core.util.Consumer",
     *      缺 `androidx.core:core-ktx` 依赖 (报错不会告诉你缺哪个依赖)。
     *   ③ remove 的入参是 `Consumer`, 不是 Activity/Executor。
     */
    fun start() {
        if (adapter != null) return
        val a = WindowInfoTrackerCallbackAdapter(WindowInfoTracker.getOrCreate(activity))
        adapter = a
        val cb = Consumer<WindowLayoutInfo> { info ->
            last = info
            report(info, activity.resources.configuration)
        }
        listener = cb
        try {
            a.addWindowLayoutInfoListener(activity, executor, cb)
        } catch (e: Exception) {
            // 老设备 / 无扩展的模拟器上这条流可能是空的, 不该把应用带走。
            Log.w(TAG, "windowLayoutInfo 监听失败 (折叠屏能力不可用)", e)
        }
    }

    /** 会话结束 (onDestroy): 摘掉监听。不摘会让回调打在已销毁的 Activity 上。 */
    fun stop() {
        val a = adapter ?: return
        val cb = listener
        adapter = null
        listener = null
        last = null
        if (a == null || cb == null) return
        try {
            a.removeWindowLayoutInfoListener(cb)
        } catch (e: Exception) {
            Log.w(TAG, "移除 windowLayoutInfo 监听失败", e)
        }
    }

    /**
     * 用最近一次窗口布局信息复报 (onConfigurationChanged 用)。没收到过就补一次
     * "无折痕" —— 那正是直板机 / 折叠屏外屏该有的答案。
     */
    fun reportLast(config: Configuration) {
        val info = last
        if (info != null) {
            report(info, config)
            return
        }
        // 还没收到 Flow 的第一条: 至少把尺寸/尺寸类报上去, 别让内核拿到上一块屏的。
        val metrics = activity.resources.displayMetrics
        val payload = JSONObject()
        payload.put("width", metrics.widthPixels)
        payload.put("height", metrics.heightPixels)
        payload.put("widthDp", config.screenWidthDp)
        payload.put("heightDp", config.screenHeightDp)
        payload.put("sizeClass", JSONObject().apply {
            put("width", widthClassOf(config))
            put("height", heightClassOf(config))
        })
        GoxRuntime.nativeSetDisplayFold(payload.toString())
    }

    /**
     * 把一次窗口布局信息报给引擎。
     *
     * 为什么会收到**不止一条** FoldingFeature: 三折叠 / 多折设备可能有多个。
     * 这里全部如实报上去, 但 `hinge` 只取第一条 —— 内核的 `Display.Hinge` 是
     * 单折痕模型 (表达力上限), 多折痕留到真有设备用到时再扩字段。
     */
    fun report(info: WindowLayoutInfo, config: Configuration) {
        val metrics = activity.resources.displayMetrics
        // 设备像素 = dp * density。displayMetrics 在 configChanges 含 density
        // 的前提下 (见 AndroidManifest) 会随配置变更更新, 不需要自己重算。
        val w = metrics.widthPixels
        val h = metrics.heightPixels

        val payload = JSONObject()
        payload.put("width", w)
        payload.put("height", h)
        // 尺寸类: 用 Configuration 的 screenLayout 位, 与内核的 compact/medium/
        // expanded 三档对齐 —— 但**阈值判定留给内核** (gfx/viewport.go 的
        // deriveSizeClasses 是唯一真源; 这里只报 dp 供它算)。
        payload.put("sizeClass", JSONObject().apply {
            put("width", widthClassOf(config))
            put("height", heightClassOf(config))
        })
        payload.put("widthDp", config.screenWidthDp)
        payload.put("heightDp", config.screenHeightDp)

        val folds = info.displayFeatures.filterIsInstance<FoldingFeature>()
        if (folds.isNotEmpty()) {
            // 姿态: 只要有任一条折痕处于 HALF_OPENED, 设备就是折着的。
            payload.put("posture", if (folds.any { it.state == FoldingFeature.State.HALF_OPENED })
                "half-open" else "flat")

            val regions = JSONArray()
            var divIdx = 0
            var occIdx = 0
            for (f in folds) {
                // isSeparating=true ⇒ 铰链把窗口**切开**成两个逻辑区域 ⇒ division;
                // false ⇒ 只是遮住一条 (如某些内折设备的窄铰链) ⇒ occlusion。
                val kind = if (f.isSeparating) "division" else "occlusion"
                val id = if (f.isSeparating) "fold-${divIdx++}" else "occlusion-${occIdx++}"
                regions.put(JSONObject().apply {
                    put("id", id)
                    put("kind", kind)
                    put("x", f.bounds.left)
                    put("y", f.bounds.top)
                    put("width", f.bounds.width())
                    put("height", f.bounds.height())
                    // active 的语义与 iOS 侧对齐: 折着才 active, 全展时 inactive
                    // (但**仍然报上去**) —— 脚本侧 hasFold() 因此恒为 true,
                    // 列数不会在折叠/展开之间来回跳。
                    put("active", f.state == FoldingFeature.State.HALF_OPENED)
                })
            }
            payload.put("regions", regions)

            // hinge: 第一条折痕的几何 (单折痕模型)。
            val first = folds.first()
            payload.put("hinge", JSONObject().apply {
                put("x", first.bounds.left)
                put("y", first.bounds.top)
                put("width", first.bounds.width())
                put("height", first.bounds.height())
                put("orientation", orientationOf(first))
            })
        } else {
            // 没有任何 FoldingFeature ⇒ 普通直板机 (或折叠屏的**外屏**:
            // 外屏不跨铰链, 系统不报折痕)。不写 posture, 交给内核判 flat +
            // Foldable=false ⇒ 分栏退回按宽度类判。
        }

        GoxRuntime.nativeSetDisplayFold(payload.toString())
    }

    /**
     * FoldingFeature.orientation → 内核的 DisplayHinge.Orientation。
     *
     * `VERTICAL` = **折痕线是竖的** (把窗口切成左右两半) ⇒ "vertical";
     * `HORIZONTAL` = 折痕线是横的 (上下 / 桌面模式) ⇒ "horizontal"。
     * 这里与 gfx/screen.go 的语义逐字一致 —— 写反了避让带会跑到另一个轴上。
     */
    private fun orientationOf(f: FoldingFeature): String =
        when (f.orientation) {
            FoldingFeature.Orientation.VERTICAL -> "vertical"
            FoldingFeature.Orientation.HORIZONTAL -> "horizontal"
            else -> "vertical"
        }

    /** Configuration.screenLayout 的宽度档 → 内核词汇。 */
    private fun widthClassOf(config: Configuration): String =
        when (config.screenLayout and Configuration.SCREENLAYOUT_SIZE_MASK) {
            Configuration.SCREENLAYOUT_SIZE_SMALL,
            Configuration.SCREENLAYOUT_SIZE_NORMAL -> "compact"
            Configuration.SCREENLAYOUT_SIZE_LARGE -> "medium"
            Configuration.SCREENLAYOUT_SIZE_XLARGE -> "expanded"
            else -> "compact"      // UNDEFINED: 不谎报大屏
        }

    /** 高度档: 系统只有 SCREENLAYOUT_LONG (比标准更高) 一个位可用。 */
    private fun heightClassOf(config: Configuration): String =
        if (config.screenLayout and Configuration.SCREENLAYOUT_LONG_MASK ==
            Configuration.SCREENLAYOUT_LONG_YES) "regular" else "compact"

    private companion object {
        const val TAG = "Gox"
    }
}
