package com.gox

/**
 * 宿主回调, **由 Go 调用**。
 *
 * 线程: 这两个方法都运行在 **Go 的渲染线程**上, 不是 UI 线程 —— 实现里要碰 View/Bitmap
 * 就必须自己 post 回主线程 (见 MainActivity.flush)。
 *
 * 名字与签名是 JNI 契约的一部分: Go 侧写死了查找 `flush([I)V` 与
 * `finished(ILjava/lang/String;)V`, 改签名会让 [GoxRuntime.nativeInit] 直接返回非 0
 * ("GoxHost.flush([I)V 找不到")。
 */
interface GoxHost {

    /**
     * 把引擎刚画好的一帧刷到屏幕上。
     *
     * @param rects 脏区, 格式 `[x, y, w, h, x, y, w, h, ...]`, 空/null 表示整帧。
     *   **实现必须在返回前拷走** —— 它背后是 JNI 局部引用, Go 侧返回后即失效。
     */
    fun flush(rects: IntArray?)

    /** 脚本结束。code != 0 时 error 有值 (脚本异常或引擎初始化失败)。 */
    fun finished(code: Int, error: String?)

    /**
     * 软键盘开关 (M2): 焦点进/出 input/textarea 时内核调。
     * 没有实现也不致命 —— Go 侧找不到本方法时降级成"软键盘不可开关"。
     */
    fun imeShow(show: Boolean)

    /**
     * 编辑框状态回传 (M2): 光标前后文本 / 选区 / 光标像素矩形, **由内核调**。
     *
     * 宿主拿它实现 `InputConnection` 的文本查询 (getTextBeforeCursor /
     * getExtractedText) 与 `updateCursorAnchorInfo` —— 后者决定输入法的候选词窗
     * 贴在光标旁边还是压在屏幕底部盖住输入框。没有它输入法也能提交结果, 只是
     * 拿不到上下文、候选窗位置靠猜。
     *
     * json 字段 (内核对 gfx.IMEEditor 的序列化):
     * ```
     * {"text":"你好","selStart":2,"selEnd":2,"x":37,"y":410,"w":1,"h":14,
     *  "focused":true,"multiline":false}
     * ```
     * 偏移是**引擎口径的 rune 下标** —— Kotlin 侧要换算成 UTF-16 单元再用
     * (中文一字一单元、emoji 一字两单元, 直接当 char 下标会错位)。
     *
     * 与 [flush]/[finished] 一样运行在 **Go 的渲染线程**上: 要碰 View / 输入法
     * 必须自己 post 回主线程。没有实现也不致命 (Go 侧找不到就跳过)。
     */
    fun imeEditor(json: String)
}
