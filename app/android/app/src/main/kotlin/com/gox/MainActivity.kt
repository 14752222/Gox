package com.gox

import android.app.Activity
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Rect
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.util.Log
import android.view.Choreographer
import android.view.KeyEvent
import android.view.MotionEvent
import android.view.SurfaceHolder
import android.view.SurfaceView
import android.view.View
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
 *     等真机实测出撕裂概率再决定值不值。
 *   - 软键盘 (IME) 未接: 输入框在真机上只能看不能输 (M2, 工作量最大的一块)。
 *   - 多指手势不支持: 第二根手指按下即作废整个手势 (见 gfx/mobile.Touch 的注释)。
 */
class MainActivity : Activity(), SurfaceHolder.Callback, GoxHost {

    private val ui = Handler(Looper.getMainLooper())

    private lateinit var surfaceView: SurfaceView
    private var buffer: ByteBuffer? = null
    private var bitmap: Bitmap? = null
    private var width = 0
    private var height = 0
    private var inited = false
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
        surfaceView.setOnTouchListener { _: View, ev: MotionEvent ->
            // 直接透传 actionMasked: Go 侧按 Android 的动作值映射 (Down/Up/Move/Cancel,
            // 以及 POINTER_DOWN/UP → Cancel), 两端不各维护一份枚举。
            GoxRuntime.nativeTouch(ev.actionMasked, ev.x, ev.y)
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
    }

    private fun reportInsets(insets: android.view.WindowInsets) {
        if (!inited) return
        // systemWindowInset 系列 API 21 起就有且够用 (v1 不引 androidx);
        // 标记 @Suppress 而不是换 API: 换了就得连依赖一起升级, 不值。
        @Suppress("DEPRECATION")
        GoxRuntime.nativeSetInsets(
            insets.systemWindowInsetTop,
            insets.systemWindowInsetRight,
            insets.systemWindowInsetBottom,
            insets.systemWindowInsetLeft,
        )
    }

    // ===== SurfaceHolder.Callback =====

    override fun surfaceCreated(holder: SurfaceHolder) {
        Log.i(TAG, "surfaceCreated")
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
            window.decorView.rootWindowInsets?.let { reportInsets(it) }
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

        // lockCanvas 只接受一个脏矩形: v1 取第一个 (引擎给的脏区本来就做过合并, 常见就是 1~2 块)。
        val rect = if (dirty != null && dirty.size >= 4) {
            Rect(dirty[0], dirty[1], dirty[0] + dirty[2], dirty[1] + dirty[3])
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

    /** 焦点进/出编辑框时内核调 (Go 渲染线程), 碰输入法必须回主线程。 */
    override fun imeShow(show: Boolean) {
        ui.post {
            val imm = getSystemService(INPUT_METHOD_SERVICE) as? android.view.inputmethod.InputMethodManager
                ?: return@post
            if (show) {
                surfaceView.requestFocus()
                imm.showSoftInput(surfaceView, 0)
            } else {
                imm.hideSoftInputFromWindow(surfaceView.windowToken, 0)
            }
        }
    }

    /**
     * 能吃软键盘的 SurfaceView: 默认的 SurfaceView 没有 InputConnection, 输入法
     * 根本不弹。这里给一个 BaseInputConnection 并只接"结果提交" (与内核 v1 口径
     * 一致): 拼音组合过程由输入法自己显示, commitText 到达才转发; 组合中的
     * setComposingText 忽略; 删除走 deleteSurroundingText / KEYCODE_DEL。
     */
    private inner class GoxSurfaceView(context: android.content.Context) : SurfaceView(context) {

        override fun onCreateInputConnection(outAttrs: android.view.inputmethod.EditorInfo): android.view.inputmethod.InputConnection? {
            outAttrs.inputType = android.text.InputType.TYPE_CLASS_TEXT
            outAttrs.imeOptions = android.view.inputmethod.EditorInfo.IME_FLAG_NO_EXTRACT_UI
            return object : android.view.inputmethod.BaseInputConnection(this, false) {
                override fun commitText(text: CharSequence, newCursorPosition: Int): Boolean {
                    GoxRuntime.nativeIMECommit(text.toString())
                    return true
                }

                override fun deleteSurroundingText(beforeLength: Int, afterLength: Int): Boolean {
                    // 输入法删除走这里; 退一格就是一条 Backspace 按键事件
                    repeat(beforeLength) {
                        GoxRuntime.nativeKey("Backspace", true)
                        GoxRuntime.nativeKey("Backspace", false)
                    }
                    return true
                }

                override fun setComposingText(text: CharSequence, newCursorPosition: Int): Boolean {
                    // v1 不做内联预编辑: 组合过程留在输入法里, 到 commitText 才转发
                    return true
                }
            }
        }
    }

    // ===== 生命周期 =====

    override fun onResume() {
        super.onResume()
        startTicker()
    }

    override fun onPause() {
        // 切后台停掉 vsync 驱动: 泵会睡在 WaitEvents 里, 不空转 (M3 的省电口径)。
        stopTicker()
        super.onPause()
    }

    override fun onDestroy() {
        stopTicker()
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
