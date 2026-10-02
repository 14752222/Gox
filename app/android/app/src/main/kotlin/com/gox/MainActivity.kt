package com.gox

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.res.Configuration
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Matrix
import android.graphics.Rect
import android.os.Bundle
import android.os.Build
import android.text.InputType
import android.view.WindowInsets
import android.view.WindowManager
import android.view.inputmethod.BaseInputConnection
import android.view.inputmethod.CursorAnchorInfo
import android.view.inputmethod.EditorInfo
import android.view.inputmethod.ExtractedText
import android.view.inputmethod.ExtractedTextRequest
import android.view.inputmethod.InputConnection
import android.view.inputmethod.InputMethodManager
import android.os.Handler
import android.os.Looper
import android.util.Log
import android.view.Choreographer
import android.view.KeyEvent
import android.view.MotionEvent
import android.view.SurfaceHolder
import android.view.SurfaceView
import android.view.View
import androidx.core.view.WindowCompat
import java.nio.ByteBuffer

/**
 * 最小宿主: SurfaceView + Choreographer + 触摸 → libgox.so。
 *
 * 分工 (与 gfx/android 头部的契约一致): Kotlin 负责"窗口与输入", Go 负责"渲染与逻辑"。
 * 像素通道是 **direct ByteBuffer 单缓冲**: Go 直接往里写 RGBA, 宿主在 [flush] 里拷进
 * Bitmap 再 `drawBitmap` 上屏 —— 一次额外拷贝, 契约里明确接受。
 *
 * v1 已知边界 (都不是 bug, 是没做):
 *   - **单缓冲**: Go 写入与宿主拷贝可能重叠一帧 (撕裂)。彻底解决要双缓冲 + 交换指针,
 *     等真机实测出撕裂概率再决定值不值。这不是"只有多指时才撕" —— 单指快速拖动一样会撞上。
 *   - 仅第一根手指有效: 第二根手指按下即被忽略 (既不打断当前手势, 也不产生第二路触点)。
 *     多指缩放/旋转等手势 M2 之后再做 —— 见 gfx/mobile 的 Touch 注释。
 *   - IME 走**结果提交制**: 拼音/手写的组合过程留在系统输入法里, 只有 commitText
 *     才进编辑框; 一次提交是**一批**字符, 整批插入、光标一次跨过整批 —— 逐字插入
 *     会让中文输入的字序错乱。逐键上报属 P1, 见 docs/mobile-adaptation.md §9。
 */
/**
 * 引擎回传的编辑框快照 (GoxHost.imeEditor 的 JSON)。
 *
 * 偏移都是**引擎口径的 rune 下标**, 不是 UTF-16 单元 —— 消费方要自己过
 * [utf16IndexOf] (见 GoxSurfaceView 的文本口径注释)。
 */
private class EditorState(
    val text: String,
    val selStart: Int,
    val selEnd: Int,
    val caretX: Int,
    val caretY: Int,
    val caretW: Int,
    val caretH: Int,
    val focused: Boolean,
    val multiline: Boolean,
) {
    companion object {
        /** 焦点不在编辑框上时的缺省快照。 */
        val NONE = EditorState("", 0, 0, 0, 0, 1, 1, false, false)

        /** 解析失败返回 null (回传只是增强, 不该把宿主拖崩)。 */
        fun parse(json: String): EditorState? = try {
            val o = org.json.JSONObject(json)
            EditorState(
                text = o.optString("text", ""),
                selStart = o.optInt("selStart", 0),
                selEnd = o.optInt("selEnd", 0),
                caretX = o.optInt("x", 0),
                caretY = o.optInt("y", 0),
                caretW = o.optInt("w", 1),
                caretH = o.optInt("h", 1),
                focused = o.optBoolean("focused", false),
                multiline = o.optBoolean("multiline", false),
            )
        } catch (e: org.json.JSONException) {
            null
        }
    }
}

/**
 * rune 下标 → UTF-16 下标。
 *
 * 引擎 (Go) 按 rune 计数, 而 Java 的 String 与 InputConnection 都按 UTF-16 单元:
 * 中文一字一单元, emoji 一字两单元。这层换算少做一次, 带表情的输入就会整体错位。
 * 越界一律钳到串尾 (受控值被 JS 改短时旧光标会越界)。
 */
private fun utf16IndexOf(text: String, runeIndex: Int): Int {
    var runes = 0
    var i = 0
    while (i < text.length && runes < runeIndex) {
        i += Character.charCount(text.codePointAt(i))
        runes++
    }
    return i
}

/** 去掉最后一个 rune (可能是代理对, 不能只砍一个 char)。 */
private fun dropLastRune(s: String): String {
    if (s.isEmpty()) return s
    return s.substring(0, s.offsetByCodePoints(s.length, -1))
}


class MainActivity : Activity(), SurfaceHolder.Callback, GoxHost, GoxNativeHost {

    private val ui = Handler(Looper.getMainLooper())

    // NativeHost 六模块实现 (device/app/geo/media/permission), 本类只做转发与
    // 系统回调接线 (onActivityResult / onRequestPermissionsResult / 生命周期上报)。
    private val nativeHost = GoxNativeHostImpl(this)

    /** 折叠屏上报 (androidx.window 的 FoldingFeature → gx/screen + gx/viewport)。 */
    private val displayFold by lazy { GoxDisplayFold(this) }

    /** 电池广播接收器 (gx/device 的 battery 上报, 系统粘性广播, 主线程回调)。 */
    private val batteryReceiver = object : android.content.BroadcastReceiver() {
        override fun onReceive(ctx: Context, intent: Intent) {
            if (intent.action == Intent.ACTION_BATTERY_CHANGED) {
                nativeHost.reportBatteryFrom(intent)
            }
        }
    }

    /**
     * 表面视图。类型写成具体的 GoxSurfaceView 而不是 SurfaceView: 输入法相关
     * 的几个入口 (applyIMEWanted / onEditorState) 是它的成员, 声明成基类就拿不到。
     */
    private lateinit var surfaceView: GoxSurfaceView
    private var buffer: ByteBuffer? = null
    private var bitmap: Bitmap? = null
    private var width = 0
    private var height = 0
    private var inited = false
    /** 上一次上报的键盘高度 (像素); -1 = 还没报过。避免每次 insets 回调都跨一次 JNI。 */
    private var lastKeyboard = -1

    /** 当前跟踪的触摸点 id (见 setOnTouchListener 的单指策略)。 */
    private var activePointerId = -1

    private var ticking = false

    // FPS 观测: 移动端软光栅是 CPU 活, 先用它拿到"真的跑得动吗"的数字再谈优化。
    private var frames = 0
    private var fpsSince = 0L

    /** 宿主当驱动方: 每帧叫醒一次 Go 的事件泵。 */
    private val ticker = object : Choreographer.FrameCallback {
        override fun doFrame(frameTimeNanos: Long) {
            if (!ticking) return
            GoxRuntime.nativeTick()
            Choreographer.getInstance().postFrameCallback(this)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        surfaceView = GoxSurfaceView(this)
        surfaceView.holder.addCallback(this)
        // Edge-to-edge (insets 全量分发的前提):
        // 默认 decorFitsSystemWindows=true 时, **IME insets 被系统消费, 根本不分发**
        // 给视图树 —— onApplyWindowInsets 只在窗口 attach 时收到一次全 0, 之后键盘
        // 弹出/收起再也不回调, useKeyboardHeight() 就永远停在 0 (2026-10-02 模拟器
        // 实测)。关掉 fits 后系统栏/刘海/IME 的 insets 都从这里走, 安全区与键盘让位
        // 全部由脚本经 useInsets()/useKeyboardHeight() 决定 —— 这正是 gx/viewport
        // 的设计前提。
        WindowCompat.setDecorFitsSystemWindows(window, false)
        // 键盘弹起时**不要**让窗口自己变形: 我们的模型是"宿主报告键盘高度, 由脚本
        // 决定谁让位" (gx/viewport 的 useKeyboardHeight)。系统默认会按需 ADJUST_RESIZE,
        // 那会把 SurfaceView 一起缩小 ⇒ surfaceChanged → nativeResize ⇒ 整棵树重排,
        // 再叠加一次键盘让位, 内容会被顶掉两倍键盘高。
        window.setSoftInputMode(WindowManager.LayoutParams.SOFT_INPUT_ADJUST_NOTHING)

        // 只跟踪**第一根**手指: 第二根手指按下不再作废整个手势 (v1 曾经那样),
        // 因为"另一只手搭上来"是极常见的误触, 让它把正在拖的东西弹回去很糟。
        // 多指手势 (捏合/旋转) 仍不支持 —— 那是 v1 边界, 不是 bug。
        surfaceView.setOnTouchListener { _: View, ev: MotionEvent ->
            when (ev.actionMasked) {
                MotionEvent.ACTION_DOWN -> {
                    activePointerId = ev.getPointerId(0)
                    GoxRuntime.nativeTouch(MotionEvent.ACTION_DOWN, ev.x, ev.y)
                }
                MotionEvent.ACTION_MOVE -> {
                    val i = ev.findPointerIndex(activePointerId)
                    // 主手指不在这一批里 (被系统抢走) 就丢掉这个 Move, 不发错坐标。
                    if (i >= 0) {
                        GoxRuntime.nativeTouch(MotionEvent.ACTION_MOVE, ev.getX(i), ev.getY(i))
                    }
                }
                MotionEvent.ACTION_POINTER_DOWN -> {
                    // 第二根及以后的手指: 忽略 (不发 Cancel —— 那才是"作废手势")
                }
                MotionEvent.ACTION_POINTER_UP -> {
                    // 抬起的若是**主手指**, 才结束手势 (坐标取它在事件里的那一份)
                    val i = ev.actionIndex
                    if (ev.getPointerId(i) == activePointerId) {
                        GoxRuntime.nativeTouch(MotionEvent.ACTION_CANCEL, ev.getX(i), ev.getY(i))
                        activePointerId = -1
                    }
                }
                MotionEvent.ACTION_UP -> {
                    activePointerId = -1
                    GoxRuntime.nativeTouch(MotionEvent.ACTION_UP, ev.x, ev.y)
                }
                MotionEvent.ACTION_CANCEL -> {
                    activePointerId = -1
                    GoxRuntime.nativeTouch(MotionEvent.ACTION_CANCEL, ev.x, ev.y)
                }
                else -> GoxRuntime.nativeTouch(ev.actionMasked, ev.x, ev.y)
            }
            true
        }
        setContentView(surfaceView)
        // 安全区 → 引擎 (gx/viewport 的 insets): 状态栏/刘海/手势条的占位。
        // WindowInsets 本身就是**像素**, 与内核设备像素口径一致, 不用换算。
        // 报告时机除了这里, onSurfaceSize 里会补报一次 (listener 可能早于 nativeInit)。
        surfaceView.setOnApplyWindowInsetsListener { _, insets ->
            reportInsets(insets)
            insets // 不消费: Activity 未开 fitSystemWindows, 布局不受影响
        }

        // ── NativeHost 上报通道接线 ──
        // 电池: ACTION_BATTERY_CHANGED 是粘性系统广播, 注册即收到一份当前值 ——
        // 启动后 useBattery() 读到的就是真值而不是缺省。
        registerReceiver(batteryReceiver, IntentFilter(Intent.ACTION_BATTERY_CHANGED))
        // 网络: ConnectivityManager 回调 + 启动时先报一次当前状态。
        nativeHost.registerNetworkCallback()
        reportNetworkInitial()
    }

    /**
     * 启动时的网络初值 (activeNetwork 的 capabilities, 没有就报未连接)。
     *
     * `activeNetwork` / `getNetworkCapabilities` 需要 ACCESS_NETWORK_STATE —— normal
     * 级权限, 由 manifest 声明、安装即授 (基线权限, 见 AndroidManifest.xml), 正常不会缺。
     * 但缺了它抛的是 SecurityException, 而且是在 **Activity.onCreate 里** 抛, 表现成
     * "装完就打不开" —— 所以这里吞掉并降级成"未连接", 与 [GoxNativeHostImpl.registerNetworkCallback]
     * 的 try/catch 口径一致: 网络信息缺失不该让整个 App 起不来。
     */
    private fun reportNetworkInitial() {
        val cm = getSystemService(Context.CONNECTIVITY_SERVICE) as? android.net.ConnectivityManager ?: return
        try {
            val net = cm.activeNetwork
            val caps = if (net != null) cm.getNetworkCapabilities(net) else null
            nativeHost.reportNetwork(caps != null && caps.hasCapability(android.net.NetworkCapabilities.NET_CAPABILITY_VALIDATED), caps)
        } catch (e: SecurityException) {
            Log.w(TAG, "读网络状态被拒 (缺 ACCESS_NETWORK_STATE?): ${e.message}")
        }
    }

    // ===== GoxNativeHost (内核经 JNI 调入, Go 的脚本线程) =====

    override fun nativeCapabilities(): String = nativeHost.nativeCapabilities()

    override fun nativeCall(method: String, argsJson: String): String =
        nativeHost.nativeCall(method, argsJson)

    // ===== 系统回调 → NativeHost (主线程) =====

    @Deprecated("Deprecated in Java")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        nativeHost.onMediaResult(requestCode, resultCode, data)
    }

    override fun onRequestPermissionsResult(
        requestCode: Int,
        permissions: Array<out String>,
        grantResults: IntArray,
    ) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        nativeHost.onPermissionResult(requestCode, permissions, grantResults)
    }

    /**
     * 安全区 + 软键盘高度 → 引擎 (gx/viewport)。
     *
     * 两者是**两条独立通道**: insets 走 nativeSetInsets (安全区), 键盘高度走
     * nativeSetKeyboard —— Viewport 的合并是 upsert, 复用同一条会把安全区清成 0
     * (键盘弹起时 insets 往往没变)。内核侧 `contentArea` / `safeAreaStyle(win,true)`
     * 取两者较大者而不是相加, 所以"底部 insets 已经含键盘"的老口径不会双重让位。
     */
    private fun reportInsets(insets: android.view.WindowInsets) {
        if (!inited) return
        reportKeyboard(insets)
        // API 30+ 必须用 getInsets(systemBars|displayCutout): decorFitsSystemWindows
        // =false 之后, 旧 `systemWindowInsetTop` 一族走的是"已被消费"的兼容口径,
        // 恒返回 0 —— 2026-10-02 模拟器实测 insets 全 0 的根因。
        // (androidx 已在依赖里 —— WindowCompat 就是它, 这里直接用平台 API。)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            val bars = insets.getInsets(
                WindowInsets.Type.systemBars() or WindowInsets.Type.displayCutout()
            )
            GoxRuntime.nativeSetInsets(bars.top, bars.right, bars.bottom, bars.left)
        } else {
            @Suppress("DEPRECATION")
            GoxRuntime.nativeSetInsets(
                insets.systemWindowInsetTop,
                insets.systemWindowInsetRight,
                insets.systemWindowInsetBottom,
                insets.systemWindowInsetLeft,
            )
        }
    }

    /** 键盘高度变了才上报 (内核据此抬内容, 见 gx/viewport 的 useKeyboardHeight)。 */
    private fun reportKeyboard(insets: android.view.WindowInsets?) {
        val h = keyboardHeightOf(insets)
        if (h == lastKeyboard) return
        lastKeyboard = h
        GoxRuntime.nativeSetKeyboard(h)
    }

    /**
     * 软键盘现在占了多高 (像素; 0 = 没弹)。
     *
     * API 30+ 有独立的 IME inset (`WindowInsets.Type.ime()`), 直接取。
     * API 24~29 没有它, 而 `systemWindowInsetBottom` 把导航栏与键盘**加在一起**
     * 分不出来 —— 于是用 getWindowVisibleDisplayFrame 的差: 键盘弹起时可见帧的
     * 底边会上移, 差值就是键盘高度。15% 的阈值滤掉状态栏/导航栏抖动 (键盘不可能
     * 只占屏高的 15%)。
     */
    private fun keyboardHeightOf(insets: android.view.WindowInsets?): Int {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R && insets != null) {
            val ime = WindowInsets.Type.ime()
            return if (insets.isVisible(ime)) insets.getInsets(ime).bottom else 0
        }
        val visible = Rect()
        window.decorView.getWindowVisibleDisplayFrame(visible)
        val decorH = window.decorView.height
        if (decorH <= 0) return 0
        val hidden = decorH - visible.bottom
        return if (hidden > decorH * 15 / 100) hidden else 0
    }

    /** 输入法服务 (拿不到就返回 null —— 不影响渲染, 只是没有软键盘)。 */
    private fun imm(): InputMethodManager? =
        getSystemService(INPUT_METHOD_SERVICE) as? InputMethodManager

    // ===== SurfaceHolder.Callback =====

    override fun surfaceCreated(holder: SurfaceHolder) {
    }

    override fun surfaceDestroyed(holder: SurfaceHolder) {
        // 表面没了就别再驱动泵 (再 tick 也只是空转)
        stopTicker()
    }

    override fun surfaceChanged(holder: SurfaceHolder, format: Int, w: Int, h: Int) {
        onSurfaceSize(w, h)
        startTicker()
    }

    /**
     * 表面尺寸/密度变化。首次调用建立会话, 之后只重绑帧缓冲并报告 resize ——
     * **重绑是必须的**: 旧 ByteBuffer 已经按旧尺寸分配, 继续写会越界。
     */
    private fun onSurfaceSize(w: Int, h: Int) {
        if (w <= 0 || h <= 0) return
        val density = resources.displayMetrics.density
        width = w
        height = h
        val fresh = byteBufferOf(w, h)
        if (!inited) {
            buffer = fresh
            bitmap = Bitmap.createBitmap(w, h, Bitmap.Config.ARGB_8888)
            val rc = GoxRuntime.nativeInit(w, h, density, fresh, this)
            if (rc != 0) {
                // 非 0 = 引擎没起来 (帧缓冲不是 direct / GoxHost 签名不对, 日志里有原因)。
                // 这时候继续跑只会得到一块黑屏, 所以直接报错收工。
                Log.e(TAG, "nativeInit 失败 rc=$rc —— 引擎未启动")
                return
            }
            inited = true
            // insets 的首次分发发生在窗口 attach 时, 早于 init (inited=false 被
            // 卫语句挡掉), 之后系统不会主动再发 —— 主动请求一次, 否则启动后
            // useInsets() 全 0、脚本让不了安全区。
            surfaceView.requestApplyInsets()
            window.decorView.rootWindowInsets?.let { reportInsets(it) }
            // 折叠屏: 启动监听 (注册即拿当前值)。放在 nativeInit 之后 ——
            // 内核已在跑, 上报才有订阅者。非阻塞 (走 window-java 的回调适配器,
            // 见 GoxDisplayFold.start 的注释)。
            displayFold.start()
            runScript()
            return
        }
        if (bitmap?.width != w || bitmap?.height != h) {
            buffer = fresh
            bitmap = Bitmap.createBitmap(w, h, Bitmap.Config.ARGB_8888)
            GoxRuntime.nativeBindFrameBuffer(w, h, fresh)
        }
        GoxRuntime.nativeResize(w, h, density)
    }

    private fun byteBufferOf(w: Int, h: Int): ByteBuffer = ByteBuffer.allocateDirect(w * h * 4)

    private fun runScript() {
        val source = try {
            assets.open(SCRIPT_ASSET).bufferedReader().use { it.readText() }
        } catch (e: Exception) {
            Log.e(TAG, "读取 asset/$SCRIPT_ASSET 失败", e)
            return
        }
        Log.i(TAG, "启动脚本 $SCRIPT_ASSET (${source.length} 字符)")
        GoxRuntime.nativeRunScript(source, SCRIPT_ASSET)
    }

    // ===== GoxHost: 由 Go 在**渲染线程**上调用 =====

    override fun flush(rects: IntArray?) {
        // 先拷再 post: rects 背后是 JNI 局部引用, 回调返回后即失效; 且拷贝要在这里做,
        // 不能等 post 出去的 Runnable 拿到它。
        val dirty = rects?.copyOf()
        ui.post { blit(dirty) }
    }

    override fun finished(code: Int, error: String?) {
        Log.i(TAG, "脚本结束 code=$code error=$error")
    }

    /** 在 UI 线程把引擎的帧缓冲画到 Surface 上。 */
    private fun blit(dirty: IntArray?) {
        val holder = surfaceView.holder
        val bmp = bitmap ?: return
        val buf = buffer ?: return

        // copyPixelsFromBuffer 从 position 开始读, 上一轮读完 position 停在末尾 ⇒ 必须 rewind。
        buf.rewind()
        bmp.copyPixelsFromBuffer(buf)

        // lockCanvas 只接受**一个**脏矩形: 把引擎合并后的多块脏区取包围盒。
        // 不能只取第一块 —— 一次局部重绘常带多个脏矩形 (例: IME 提交同时改了
        // 输入框和依赖同一信号的状态文本), lockCanvas 的 clip 会把其余块**裁掉**,
        // 那些节点的内容就永远停在旧值, 直到下一次整帧重绘 (2026-10-02 模拟器
        // 实测: 输入框更新了, "输入内容: …" 标签没更新)。包围盒多刷的是块与块
        // 之间的空白, v1 接受; 引擎侧 mergeRects 已做过合并, 这里通常只有 1~2 块。
        val rect = if (dirty != null && dirty.size >= 4) {
            var l = Int.MAX_VALUE
            var t = Int.MAX_VALUE
            var r = Int.MIN_VALUE
            var b = Int.MIN_VALUE
            var i = 0
            while (i + 3 < dirty.size) {
                val x = dirty[i]
                val y = dirty[i + 1]
                val w = dirty[i + 2]
                val h = dirty[i + 3]
                if (w > 0 && h > 0) {
                    if (x < l) l = x
                    if (y < t) t = y
                    if (x + w > r) r = x + w
                    if (y + h > b) b = y + h
                }
                i += 4
            }
            if (l < r && t < b) Rect(l, t, r, b) else null
        } else {
            null
        }
        val canvas: Canvas = try {
            if (rect != null) holder.lockCanvas(rect) else holder.lockCanvas()
        } catch (e: IllegalStateException) {
            return // surface 已销毁/正在销毁, 这一帧丢掉即可
        } ?: return
        try {
            if (rect != null) {
                canvas.drawBitmap(bmp, rect, rect, null)
            } else {
                canvas.drawBitmap(bmp, 0f, 0f, null)
            }
        } finally {
            holder.unlockCanvasAndPost(canvas)
        }

        frames++
        val now = System.currentTimeMillis()
        if (fpsSince == 0L) fpsSince = now
        val span = now - fpsSince
        if (span >= 1000) {
            Log.i(TAG, "fps=${frames * 1000 / span} surface=${width}x$height")
            frames = 0
            fpsSince = now
        }
    }

    // ===== 软键盘 / IME (M2) =====

    /**
     * 焦点进/出编辑框时内核调 (**Go 渲染线程**), 碰输入法必须回主线程。
     * 引擎只表达意图 (该弹/该收), 具体怎么弹由下面的 applyIMEWanted 决定。
     */
    override fun imeShow(show: Boolean) {
        ui.post { surfaceView.applyIMEWanted(show) }
    }

    /**
     * 内核回传的编辑框状态 (M2, **Go 渲染线程**): 光标前后的文本、选区、光标
     * 像素矩形。转发给 View —— 输入法是在主线程上同步来问的。
     */
    override fun imeEditor(json: String) {
        val st = EditorState.parse(json) ?: return
        ui.post { surfaceView.onEditorState(st) }
    }

    /**
     * 能吃软键盘的 SurfaceView。
     *
     * 默认的 SurfaceView 既不可聚焦、也没有 InputConnection, 于是 showSoftInput
     * 根本不会有任何反应 —— 这里把这两样补齐, 并把输入法需要的三件事全部接到
     * 引擎上 (M2):
     *
     *   ① **文本上下文** (getTextBeforeCursor / getExtractedText / 选区与光标)
     *      —— 真源是 Go 侧每帧回传的编辑框快照 ([editor]), 本类**不**自己记账;
     *   ② **光标在屏幕上的位置** (updateCursorAnchorInfo)
     *      —— 缺它的话候选词窗只能贴在屏幕底部, 会盖住输入框本身; 这一条正是
     *      "候选词"能不能正经用的分水岭;
     *   ③ **该不该弹** (applyIMEWanted) —— 由内核按焦点调。
     *
     * 文本口径: 引擎给的是 **rune 下标** (Go 的 rune), 而 Java 的 String 与
     * InputConnection 都按 **UTF-16 单元** —— 中文一字一单元、emoji 一字两单元。
     * 换算集中在 utf16IndexOf, 少做一次就会在带表情的输入上整体错位。
     *
     * 组合态 (拼音还没选定的那一段) **只活在本类里**: 结果提交制下引擎收不到它,
     * 而输入法认为它已经在文本里 —— 所以 getTextBeforeCursor 要把它拼在光标
     * 前面, 删改也要先在组合串上消化 (见 GoxInputConnection)。逐键上报属 P1,
     * 这里刻意不越界。
     */
    private inner class GoxSurfaceView(context: android.content.Context) : SurfaceView(context) {

        /** 引擎回传的编辑框快照 (真源)。渲染线程写、主线程与输入法线程读。 */
        @Volatile
        var editor = EditorState.NONE
            private set

        /** 组合中的串 (未提交)。引擎看不到它, 输入法认为它在文本里。 */
        private var composing = ""

        /** 组合串在全文里的起点 (UTF-16 下标; -1 = 没有组合)。 */
        private var composingStart = -1

        /** 内核要求的键盘状态 (imeShow)。快照晚一帧到, 所以必须记住意图。 */
        private var imeWanted = false

        /** 输入法上一次拿到的选区 (UTF-16); 只有变了才打扰它。 */
        private var lastSelStart = -1
        private var lastSelEnd = -1

        init {
            // 没有这两行 showSoftInput 不会有任何反应 —— 系统需要"键盘给谁输入"
            // 的答案, 而不可聚焦的 View 给不出。实测症状: 点了输入框不弹键盘。
            isFocusable = true
            isFocusableInTouchMode = true
        }

        /** 内核要求开/收软键盘。主线程调。 */
        fun applyIMEWanted(show: Boolean) {
            imeWanted = show
            val imm = imm() ?: return
            if (show) {
                requestFocus()
                // restartInput: 让输入法**重新**取一次 EditorInfo / InputConnection。
                // 引擎的焦点与 View 的焦点不是一回事 (View 全程持焦), 不 restart
                // 的话输入法会拿着上一轮的 inputType (例如把多行框当单行)。
                imm.restartInput(this)
                imm.showSoftInput(this, 0)
            } else {
                imm.hideSoftInputFromWindow(windowToken, 0)
            }
        }

        /** 引擎回传了新快照。主线程调。 */
        fun onEditorState(st: EditorState) {
            val wasFocused = editor.focused
            editor = st
            val imm = imm()
            if (!st.focused) {
                composing = ""
                composingStart = -1
                lastSelStart = -1
                lastSelEnd = -1
                return
            }
            val ss = utf16IndexOf(st.text, st.selStart)
            val se = utf16IndexOf(st.text, st.selEnd)
            if (!wasFocused) {
                // 刚进编辑框: 让输入法重取上下文并尝试弹键盘。内核的 imeShow 可能
                // 早于本次快照到达 —— 那一刻 onCreateInputConnection 还答不出
                // "焦点在哪个框、几行", 所以这里重来一次。
                composing = ""
                composingStart = -1
                lastSelStart = -1
                lastSelEnd = -1
                if (imm != null) {
                    imm.restartInput(this)
                    if (imeWanted) imm.showSoftInput(this, 0)
                }
            }
            if (imm == null) return
            if (ss != lastSelStart || se != lastSelEnd) {
                lastSelStart = ss
                lastSelEnd = se
                // 告诉输入法"光标在全文的第几个 UTF-16 单元" —— 全选/翻页/联想
                // 都要用, 只有锚点是不够的。
                imm.updateSelection(this, ss, se, -1, -1)
            }
            updateCursorAnchor(st, imm)
        }

        /** 组合串/快照变化后立刻重报一次锚点 (输入法据此摆预编辑与候选窗)。 */
        private fun refreshAnchor() {
            val imm = imm() ?: return
            updateCursorAnchor(editor, imm)
        }

        /**
         * 把光标矩形告诉输入法。这是"候选词"能不能用的关键: 输入法据此把候选
         * 词窗摆在光标附近, 而不是固定在屏幕底部盖住输入框。
         *
         * 坐标是**窗口像素**, 与引擎给的一致 (SurfaceView 铺满窗口, 不必再换算)。
         */
        private fun updateCursorAnchor(st: EditorState, imm: InputMethodManager) {
            if (!st.focused || st.caretW <= 0 || st.caretH <= 0) return
            val builder = CursorAnchorInfo.Builder()
                // setMatrix 必须在**带位置参数**时给 (setInsertionMarkerLocation 就是):
                // 缺了它 Builder.build() 直接抛 IllegalArgumentException 把 App 崩掉
                // (API 的硬校验, 2026-10-02 模拟器实测)。我们不缩放/平移窗口, 给单位阵。
                .setMatrix(Matrix())
                .setSelectionRange(
                    utf16IndexOf(st.text, st.selStart),
                    utf16IndexOf(st.text, st.selEnd),
                )
            if (composing.isNotEmpty() && composingStart >= 0) {
                builder.setComposingText(composingStart, composing)
            }
            val top = st.caretY.toFloat()
            val bottom = (st.caretY + st.caretH).toFloat()
            builder.setInsertionMarkerLocation(
                st.caretX.toFloat(), top, bottom, bottom,
                CursorAnchorInfo.FLAG_HAS_VISIBLE_REGION,
            )
            imm.updateCursorAnchorInfo(this, builder.build())
        }

        /**
         * 给输入法一个连接。**焦点不在编辑框上时返回 null** ——
         * 那是"引擎认为现在不该输入"最干净的表达 (返回一个空连接会让输入法一直
         * 挂着候选栏)。
         */
        override fun onCreateInputConnection(outAttrs: EditorInfo): InputConnection? {
            val st = editor
            if (!st.focused) return null
            outAttrs.inputType = if (st.multiline) {
                InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_MULTI_LINE
            } else {
                InputType.TYPE_CLASS_TEXT
            }
            outAttrs.imeOptions = if (st.multiline) {
                // 多行框里 Enter 必须是换行, 不能被当成"完成"
                EditorInfo.IME_FLAG_NO_ENTER_ACTION or EditorInfo.IME_FLAG_NO_EXTRACT_UI
            } else {
                // 单行框给一个"完成"键; 不要全屏编辑模式 (横屏时系统会开一个
                // 占满屏的编辑框, 而我们的界面是自己画的, 那会与引擎画面打架)。
                EditorInfo.IME_ACTION_DONE or EditorInfo.IME_FLAG_NO_EXTRACT_UI
            }
            outAttrs.initialSelStart = utf16IndexOf(st.text, st.selStart)
            outAttrs.initialSelEnd = utf16IndexOf(st.text, st.selEnd)
            outAttrs.initialCapsMode = 0
            return GoxInputConnection()
        }

        /**
         * 引擎编辑框的 InputConnection 视图。
         *
         * 全部读写落在 [editor] (引擎快照) 与 [composing] (本地组合串) 上 ——
         * 本类**不做第二份文本记账**: 引擎是受控模型, 文本真源是 JS 的 value
         * prop, 再记一份必然与它漂移, 而漂移的症状 (输入法拿旧文本做联想) 极难查。
         *
         * fullEditor = false: 不要 BaseInputConnection 那套基于 Editable 的
         * 默认实现 (它会自己维护一份可编辑缓冲并与引擎打架), 需要的几个查询
         * 全部显式实现。
         */
        private inner class GoxInputConnection : BaseInputConnection(this@GoxSurfaceView, false) {

            private fun baseText(): String = editor.text

            private fun caretStart(): Int = utf16IndexOf(baseText(), editor.selStart)

            private fun caretEnd(): Int = utf16IndexOf(baseText(), editor.selEnd)

            override fun getTextBeforeCursor(n: Int, flags: Int): CharSequence {
                // 组合串就在光标处, 且输入法认为它已经在文本里 ⇒ 必须拼在光标前,
                // 否则输入法会以为自己的预编辑被吞掉 (候选栏闪烁 / 联想错乱)。
                val s = baseText().substring(0, caretStart()) + composing
                return if (n <= 0 || n >= s.length) s else s.substring(s.length - n)
            }

            override fun getTextAfterCursor(n: Int, flags: Int): CharSequence {
                val s = baseText().substring(caretEnd())
                return if (n <= 0 || n >= s.length) s else s.substring(0, n)
            }

            override fun getSelectedText(flags: Int): CharSequence? {
                val t = baseText()
                val a = caretStart()
                val b = caretEnd()
                return if (b > a) t.substring(a, b) else null
            }

            override fun getCursorCapsMode(reqModes: Int): Int = 0

            override fun getExtractedText(request: ExtractedTextRequest, flags: Int): ExtractedText {
                val t = baseText()
                val a = caretStart()
                val out = ExtractedText()
                // 组合串还没进引擎, 但对外要看得见它 —— 否则输入法认为预编辑丢了。
                out.text = t.substring(0, a) + composing + t.substring(caretEnd())
                out.startOffset = 0
                out.selectionStart = a + composing.length
                out.selectionEnd = out.selectionStart
                out.partialStartOffset = -1
                out.partialEndOffset = -1
                out.flags = 0
                return out
            }

            override fun setComposingText(text: CharSequence, newCursorPosition: Int): Boolean {
                // 结果提交制: 组合串只留在本地, **不**送进引擎 (逐键上报属 P1)。
                // 但必须记下来 —— 输入法认为它已经插入, 之后 del/commit 都是相对
                // 它而言的。
                composing = text.toString()
                composingStart = caretStart()
                refreshAnchor()
                return true
            }

            override fun setComposingRegion(start: Int, end: Int): Boolean {
                // 没有独立的"给已有文本划组合区间"语义 (结果提交制下无未提交文本)
                return false
            }

            override fun finishComposingText(): Boolean {
                // 有些输入法不调 commitText, 而是 setComposingText("好") 之后直接
                // finishComposingText —— 那时组合串就成了正式文本。不补一次提交,
                // 用户选的字会凭空消失。
                if (composing.isNotEmpty()) {
                    val s = composing
                    composing = ""
                    composingStart = -1
                    GoxRuntime.nativeIMECommit(s)
                }
                return true
            }

            override fun commitText(text: CharSequence, newCursorPosition: Int): Boolean {
                // composing 非空说明这次提交是**替换**组合串 —— 而组合串从来没进过
                // 引擎, 所以直接提交即可, 不需要先删。
                composing = ""
                composingStart = -1
                GoxRuntime.nativeIMECommit(text.toString())
                return true
            }

            override fun deleteSurroundingText(beforeLength: Int, afterLength: Int): Boolean {
                // 组合中的删除先消化在本地组合串上: 那些字符引擎根本不知道。
                if (composing.isNotEmpty()) {
                    var c = composing
                    var n = beforeLength
                    while (n > 0 && c.isNotEmpty()) {
                        c = dropLastRune(c)
                        n--
                    }
                    composing = c
                    if (c.isEmpty()) composingStart = -1
                    refreshAnchor()
                    return true
                }
                // 真正改引擎: 退格/删除各派一次按键事件, 走既有路径 (与手敲键盘
                // 完全同路, 不新开"直接改值"的后门)。
                repeat(beforeLength) {
                    GoxRuntime.nativeKey("Backspace", true)
                    GoxRuntime.nativeKey("Backspace", false)
                }
                repeat(afterLength) {
                    GoxRuntime.nativeKey("Delete", true)
                    GoxRuntime.nativeKey("Delete", false)
                }
                return true
            }

            override fun deleteSurroundingTextInCodePoints(beforeLength: Int, afterLength: Int): Boolean =
                deleteSurroundingText(beforeLength, afterLength)

            override fun setSelection(start: Int, end: Int): Boolean {
                // 内核 v1 没有范围选择, 也没有"把光标放到任意位置"的入口。
                // 返回 false = 这个动作做不到, 输入法会退回自己的处理。
                return false
            }

            override fun performEditorAction(actionCode: Int): Boolean {
                // "完成/前往/下一个"在 v1 一律等价于"敲一次回车", 由引擎的既有
                // 按键路径决定它的含义 (单行框多半什么都不做, 多行框是换行)。
                when (actionCode) {
                    EditorInfo.IME_ACTION_DONE,
                    EditorInfo.IME_ACTION_GO,
                    EditorInfo.IME_ACTION_NEXT,
                    EditorInfo.IME_ACTION_SEND,
                    -> {
                        GoxRuntime.nativeKey("Enter", true)
                        GoxRuntime.nativeKey("Enter", false)
                        return true
                    }
                }
                return super.performEditorAction(actionCode)
            }
        }

        /** 场景: 输入法在组合态下留了残影时, 由内核要求收键盘 */
        override fun onWindowFocusChanged(hasWindowFocus: Boolean) {
            super.onWindowFocusChanged(hasWindowFocus)
            if (!hasWindowFocus) {
                composing = ""
                composingStart = -1
                return
            }
            // 回到窗口: 若引擎仍要求键盘 (例如从后台切回来), 补一次
            if (imeWanted && editor.focused) {
                imm()?.let {
                    it.restartInput(this)
                    it.showSoftInput(this, 0)
                }
            }
        }
    }

    // ===== 生命周期 =====

    override fun onResume() {
        super.onResume()
        startTicker()
        // gx/app: 前后台由宿主上报 (native_app.go 的分工)。
        GoxRuntime.nativeReportAppState("active")
        // gx/permission: 用户可能刚从设置页回来 —— 全量重报一遍 (内核状态没变
        // 时不会惊动订阅)。
        nativeHost.reportAllPermissions()
    }

    override fun onPause() {
        // 切后台停掉 vsync 驱动: 泵会睡在 WaitEvents 里, 不空转 (M3 的省电口径)。
        stopTicker()
        GoxRuntime.nativeReportAppState("background")
        super.onPause()
    }

    @Deprecated("Deprecated in Java")
    override fun onBackPressed() {
        // gx/app onBackPress: 脚本回调返回真值 = 已处理, 宿主不退出
        // (native_app.go ReportBackPress 的用法示例)。同步等脚本答复 (≤500ms)。
        if (!GoxRuntime.nativeReportBackPress()) {
            super.onBackPressed()
        }
    }

    override fun onTrimMemory(level: Int) {
        super.onTrimMemory(level)
        // gx/app 内存警告: TRIM_MEMORY_RUNNING_LOW 及更严重级别都值得告知脚本。
        if (level >= TRIM_MEMORY_RUNNING_LOW) {
            GoxRuntime.nativeReportMemoryWarning()
        }
    }

    /**
     * 配置变更回调 (清单里**故意**声明了一长串 configChanges, 见 AndroidManifest):
     * 旋转 / 折叠 / 分屏 / 内外屏密度切换都不重建 Activity —— 重建会把 Go 侧
     * 那段会话整个作废, 表现为"一折屏脚本从头开始跑"。
     *
     * 这里做两件事:
     *   1. 尺寸/密度真变了就让 onSurfaceSize 走一遍 (重绑帧缓冲 + nativeResize);
     *   2. 报一次折叠状态 —— 尺寸类与折痕可能一起变了 (内外屏切换时不仅
     *      分辨率变, 折痕的有无也会变)。用最近一次 WindowLayoutInfo 复报,
     *      WindowInfoTracker 的 Flow 自己也会因为窗口变化推一条新的。
     */
    override fun onConfigurationChanged(newConfig: Configuration) {
        super.onConfigurationChanged(newConfig)
        if (!inited) return
        val w = resources.displayMetrics.widthPixels
        val h = resources.displayMetrics.heightPixels
        if (w != width || h != height) {
            onSurfaceSize(w, h)
        } else {
            // 表面尺寸没变但**密度**可能变了 (内外屏密度不同是折叠屏的常见情况)。
            GoxRuntime.nativeResize(w, h, resources.displayMetrics.density)
        }
        displayFold.reportLast(newConfig)
    }

    override fun onDestroy() {
        stopTicker()
        nativeHost.stopAllWatches()
        displayFold.stop()
        unregisterReceiver(batteryReceiver)
        if (inited) {
            GoxRuntime.nativeDestroy()
            inited = false
        }
        ui.removeCallbacksAndMessages(null)
        super.onDestroy()
    }

    // ===== 外接键盘 (软键盘/IME 是 M2) =====

    override fun onKeyDown(keyCode: Int, event: KeyEvent): Boolean = dispatchKey(event, true)

    override fun onKeyUp(keyCode: Int, event: KeyEvent): Boolean = dispatchKey(event, false)

    /**
     * 把按键交给引擎, **只对"真的送过去了"的键返回 true**。
     *
     * 返回键有两个坑, 都实测踩过 (2026-09-24):
     *   1. 一律 return true 会把 BACK 吃掉, 用户退不出应用;
     *   2. 对 BACK `return false` 也不行 —— `onBackPressed()` 活在
     *      `Activity.onKeyDown` 的**默认分支**里, 自己 override 之后不走 super
     *      就把这条路径整个跳过了, 症状同样是返回键失效。
     * 正确写法只有一种: BACK 交给 super 的默认实现, 引擎认不出的键才 return false
     * (默认实现对那些键本来也返回 false, 音量键由框架处理, 行为一致)。
     */
    private fun dispatchKey(ev: KeyEvent, down: Boolean): Boolean {
        if (ev.keyCode == KeyEvent.KEYCODE_BACK) {
            return if (down) super.onKeyDown(ev.keyCode, ev) else super.onKeyUp(ev.keyCode, ev)
        }
        val name = keyName(ev)
        if (name.isEmpty()) return false
        GoxRuntime.nativeKey(name, down)
        return true
    }

    /** 键名与 gfx 的 Event.Key 口径一致 (Enter/Backspace/ArrowLeft/... 或单个可打印字符)。 */
    private fun keyName(ev: KeyEvent): String = when (ev.keyCode) {
        KeyEvent.KEYCODE_ENTER -> "Enter"
        KeyEvent.KEYCODE_DEL -> "Backspace"
        KeyEvent.KEYCODE_FORWARD_DEL -> "Delete"
        KeyEvent.KEYCODE_ESCAPE -> "Escape"
        KeyEvent.KEYCODE_TAB -> "Tab"
        KeyEvent.KEYCODE_DPAD_LEFT -> "ArrowLeft"
        KeyEvent.KEYCODE_DPAD_RIGHT -> "ArrowRight"
        KeyEvent.KEYCODE_DPAD_UP -> "ArrowUp"
        KeyEvent.KEYCODE_DPAD_DOWN -> "ArrowDown"
        else -> {
            val cp = ev.unicodeChar
            if (cp != 0) String(Character.toChars(cp)) else ""
        }
    }

    private fun startTicker() {
        if (ticking) return
        ticking = true
        Choreographer.getInstance().postFrameCallback(ticker)
    }

    private fun stopTicker() {
        if (!ticking) return
        ticking = false
        Choreographer.getInstance().removeFrameCallback(ticker)
    }

    private companion object {
        const val TAG = "Gox"
        const val SCRIPT_ASSET = "app.js"
    }
}
