//
//  GoxHost.swift
//  Gox
//
//  与 gfx/ios 头部契约对应的 Swift 侧装配: 视图 + 帧缓冲 + 回调。
//

import UIKit

/// 最小宿主: UIView + CADisplayLink + 触摸 → libgox.a。
///
/// 分工 (与 gfx/android 头部的契约一致): Swift 负责"窗口与输入", Go 负责"渲染与逻辑"。
/// 像素通道是 **单缓冲**: Go 直接往里写 RGBA, 宿主在 flush 回调里拷出 CGImage
/// 上屏 —— 一次额外拷贝, 与 Android 壳同一取舍。
///
/// v1 已知边界 (都不是 bug, 是没做):
///   - 软键盘 (IME) 未接: 输入框在真机上只能看不能输 (M2, 与 Android 壳同步)。
///   - 多指手势不支持: gfx/mobile.Touch 把第二根手指按 Cancel 处理。
///   - 屏幕旋转走 gox_resize + 重绑缓冲, 未做方向锁外的动画过渡。
final class GoxViewController: UIViewController {

    private var buffer: UnsafeMutableRawPointer?
    private var bufW = 0
    private var bufH = 0
    private var inited = false
    private var displayLink: CADisplayLink?
    /// 引擎当前使用的像素比 (displayScale 变化时要重绑缓冲)
    private var currentScale: CGFloat = 0

    /// 渲染像素比: 用 traitCollection.displayScale 而不是 contentScaleFactor ——
    /// 后者在 view 首次布局 (还没进窗口层级) 时可能返回 1, 等 view 真正挂上
    /// 窗口又变回 3, 造成"缓冲按 1x 建、layer 按 3x 显示"的 1/3 缩放画面
    /// (实测: 整个界面缩在左上角一小块)。
    private var renderScale: CGFloat {
        let s = view.traitCollection.displayScale
        return s > 0 ? s : UIScreen.main.scale
    }

    // MARK: - 生命周期

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = .white
        // 注意: 真正的 gox_init 不能放 viewDidLoad —— 此刻 view 还没进窗口
        // 层级, 尺寸/scale 都不可靠。挪到 viewDidLayoutSubviews 首次布局后。
    }

    override func viewDidLayoutSubviews() {
        super.viewDidLayoutSubviews()
        let s = renderScale
        view.layer.contentsScale = s
        if !inited, view.bounds.width > 0, view.bounds.height > 0 {
            startEngine(scale: s)
        } else if inited, s != currentScale {
            // scale 变化 (如 view 挂上 Retina 窗口): 按新像素比重绑缓冲与尺寸
            let w = Int(view.bounds.width * s)
            let h = Int(view.bounds.height * s)
            resizeBuffer(pixelWidth: w, pixelHeight: h)
            gox_bind_frame_buffer(Int32(w), Int32(h), buffer)
            gox_resize(Int32(w), Int32(h), Float(s))
            currentScale = s
        }
    }

    /// 惰性启动引擎: 等 view 进入窗口层级、scale/尺寸都确定之后调一次。
    private func startEngine(scale: CGFloat) {
        currentScale = scale
        resizeBuffer(pixelWidth: Int(view.bounds.width * scale),
                     pixelHeight: Int(view.bounds.height * scale))

        let rc = gox_init(Int32(bufW), Int32(bufH), Float(scale), buffer,
                          hostContext, flushFnPtr, hostContext, finishedFnPtr)
        if rc != 0 {
            alert("Gox 初始化失败 (看控制台日志)")
            return
        }
        inited = true
        reportSafeAreaInsets()
        setupIME()

        // 加载并运行随包脚本 (脚本里 render() 挂窗口树, 首帧经 flush 上屏)
        if let url = Bundle.main.url(forResource: "app", withExtension: "js"),
           let src = try? String(contentsOf: url) {
            src.withCString { s in
                "ios-app".withCString { n in
                    gox_run_script(UnsafeMutablePointer(mutating: s),
                                   UnsafeMutablePointer(mutating: n))
                }
            }
        } else {
            alert("找不到 app.js (Build Phase 没拷进 bundle?)")
            return
        }

        startTicker()
    }

    // MARK: - 安全区

    /// 把当前 safeAreaInsets (点 → 设备像素) 报给引擎 (gx/viewport 的 insets)。
    ///
    /// 两个触发时机都要: ① viewSafeAreaInsetsDidChange (启动后首次布局、
    /// 旋转/分屏) —— 但它可能早于 gox_init, 那时只能吞掉; ② startEngine
    /// 成功后补报一次, 保证引擎拿到的第一份 insets 就是启动时的真实值。
    private func reportSafeAreaInsets() {
        let s = renderScale
        let i = view.safeAreaInsets
        gox_set_insets(Int32(i.top * s), Int32(i.right * s),
                       Int32(i.bottom * s), Int32(i.left * s))
    }

    override func viewSafeAreaInsetsDidChange() {
        super.viewSafeAreaInsetsDidChange()
        guard inited else { return }
        reportSafeAreaInsets()
    }

    override func viewWillTransition(to size: CGSize, with coordinator: UIViewControllerTransitionCoordinator) {
        super.viewWillTransition(to: size, with: coordinator)
        guard inited else { return }
        coordinator.animate(alongsideTransition: { _ in
            let scale = self.renderScale
            let w = Int(size.width * scale)
            let h = Int(size.height * scale)
            self.resizeBuffer(pixelWidth: w, pixelHeight: h)
            gox_bind_frame_buffer(Int32(w), Int32(h), self.buffer)
            gox_resize(Int32(w), Int32(h), Float(scale))
        })
    }

    override func viewWillDisappear(_ animated: Bool) {
        super.viewWillDisappear(animated)
        stopTicker()
        // 回到桌面/切页时把软键盘一起收掉 (引擎的 SetIMEEnabled(false) 只在
        // 焦点变化时调, 这里是宿主自发的隐藏时机)。
        imeField?.resignFirstResponder()
    }

    override func viewWillAppear(_ animated: Bool) {
        super.viewWillAppear(animated)
        if inited { startTicker() }
    }

    deinit {
        stopTicker()
        if let b = buffer { b.deallocate() }
        gox_destroy()
    }

    // MARK: - 帧驱动

    /// 宿主当驱动方: 每帧叫醒一次 Go 的事件泵 (与 Android 的 Choreographer 同角色)。
    private func startTicker() {
        guard displayLink == nil else { return }
        let link = CADisplayLink(target: self, selector: #selector(step))
        link.add(to: .main, forMode: .common)
        displayLink = link
    }

    private func stopTicker() {
        displayLink?.invalidate()
        displayLink = nil
    }

    @objc private func step() {
        gox_tick()
    }

    // MARK: - 触摸

    override func touchesBegan(_ touches: Set<UITouch>, with event: UIEvent?) {
        sendTouch(0, touches: touches)   // gfx/mobile.TouchDown
    }

    override func touchesMoved(_ touches: Set<UITouch>, with event: UIEvent?) {
        sendTouch(1, touches: touches)   // TouchMove
    }

    override func touchesEnded(_ touches: Set<UITouch>, with event: UIEvent?) {
        sendTouch(2, touches: touches)   // TouchUp
    }

    override func touchesCancelled(_ touches: Set<UITouch>, with event: UIEvent?) {
        sendTouch(3, touches: touches)   // TouchCancel
    }

    private func sendTouch(_ action: Int32, touches: Set<UITouch>) {
        guard let t = touches.first, inited else { return }
        let scale = renderScale
        let p = t.location(in: view)
        gox_touch(action, Float(p.x * scale), Float(p.y * scale))
    }

    // MARK: - 软键盘 / IME

    /// 承载软键盘输入的隐藏文本框。
    ///
    /// 为什么不用 UITextInput 协议自己实现一套: v1 内核只做"结果提交"
    /// (gfx/ime.go 头注释) —— 拼音组合过程由系统在 field 里显示, 我们只在
    /// 组合结束 (markedTextRange 清空) 后把**已提交文本**整批发给引擎。
    /// 这个取舍换来: 组合窗、候选栏、光标、长按选择全部是系统原生行为。
    private var imeField: GoxField?

    /// gox_init 之后调: 建隐藏输入框 + 把软键盘开关回调绑给引擎。
    /// fn 会在 **Go 的 GUI 线程**上被调 (内核焦点切换), UIKit 操作必须回主队列。
    fileprivate func setupIME() {
        let f = GoxField(frame: CGRect(x: 0, y: view.bounds.height + 40, width: 10, height: 10))
        f.autocorrectionType = .no
        f.spellCheckingType = .no
        f.target = self
        f.addTarget(self, action: #selector(imeEditingChanged), for: .editingChanged)
        view.addSubview(f)
        imeField = f

        let ctx = Unmanaged.passUnretained(self).toOpaque()
        let fn = unsafeBitCast(GoxViewController.onIME, to: UnsafeMutableRawPointer.self)
        gox_bind_ime(ctx, fn)
    }

    /// 内核焦点进/出编辑框 → 开/收软键盘。在 Go 线程被调, 必须 dispatch 主队列。
    private static let onIME: @convention(c) (UnsafeMutableRawPointer?, Int32) -> Void = { ctx, on in
        guard let ctx else { return }
        let vc = Unmanaged<GoxViewController>.fromOpaque(ctx).takeUnretainedValue()
        DispatchQueue.main.async {
            if on != 0 {
                vc.imeField?.becomeFirstResponder()
            } else {
                vc.imeField?.resignFirstResponder()
            }
        }
    }

    /// 软键盘提交了一批文本 (拼音组合结束 / 直接键入) → 整批发给引擎。
    /// 在主线程被调 (UIKit 回调)。
    fileprivate func imeCommit(_ text: String) {
        text.withCString { p in
            gox_ime_commit(UnsafeMutablePointer(mutating: p))
        }
    }

    /// 退格: 空框上按删除不会有文本变化, 引擎侧光标移动靠这个补。
    fileprivate func imeBackspace() {
        // gox_key 要可变 C 指针, withCString 给的是 const —— strdup/free 绕开。
        guard let p = strdup("Backspace") else { return }
        gox_key(p, 1)
        gox_key(p, 0)
        free(p)
    }

    // MARK: - 帧缓冲

    private func resizeBuffer(pixelWidth: Int, pixelHeight: Int) {
        if let b = buffer, bufW == pixelWidth, bufH == pixelHeight { return }
        if let b = buffer { b.deallocate() }
        bufW = max(pixelWidth, 1)
        bufH = max(pixelHeight, 1)
        buffer = UnsafeMutableRawPointer.allocate(byteCount: bufW * bufH * 4,
                                                  alignment: MemoryLayout<UInt32>.alignment)
    }

    // MARK: - 宿主回调 (C 函数指针, 经 ctx 还原 self)

    /// ctx 是 Unmanaged 包住的 self —— @convention(c) 闭包不能捕获, 全靠它。
    private var hostContext: UnsafeMutableRawPointer {
        Unmanaged.passUnretained(self).toOpaque()
    }

    /// 函数指针经 unsafeBitCast 转成地址传给 gox_init。
    /// 闭包本体是**无捕获**的 @convention(c) —— 编译期就固定了地址, 无生命周期问题。
    private var flushFnPtr: UnsafeMutableRawPointer {
        unsafeBitCast(GoxViewController.onFlush, to: UnsafeMutableRawPointer.self)
    }

    private var finishedFnPtr: UnsafeMutableRawPointer {
        unsafeBitCast(GoxViewController.onFinished, to: UnsafeMutableRawPointer.self)
    }

    /// Go 渲染线程调: 先在**当前线程**整帧拷出 (此刻 Go 不会写缓冲 —— flush 是
    /// 同步的, 下一次写入要等事件泵转回来), 再丢回主队列传给 layer。
    private static let onFlush: @convention(c) (UnsafeMutableRawPointer?, UnsafePointer<Int32>?, Int32) -> Void = { ctx, _, _ in
        guard let ctx else { return }
        let vc = Unmanaged<GoxViewController>.fromOpaque(ctx).takeUnretainedValue()
        guard let buf = vc.buffer, vc.bufW > 0, vc.bufH > 0 else { return }

        let data = Data(bytes: buf, count: vc.bufW * vc.bufH * 4)  // 一次整帧拷贝 (契约接受)
        DispatchQueue.main.async {
            vc.present(data: data)
        }
        // rects 参数暂未用 (单缓冲整帧上屏, 语义仍正确); 留在契约里等脏区优化。
    }

    private static let onFinished: @convention(c) (UnsafeMutableRawPointer?, Int32, UnsafePointer<CChar>?) -> Void = { ctx, code, msg in
        guard let ctx else { return }
        let vc = Unmanaged<GoxViewController>.fromOpaque(ctx).takeUnretainedValue()
        let text = msg.map { String(cString: $0) } ?? ""
        DispatchQueue.main.async {
            if code != 0 { vc.alert("脚本错误: \(text)") }
        }
    }

    /// 把拷出来的整帧变成 CGImage 挂到 layer (contentsGravity 默认 resize,
    /// contentsScale 已在 viewDidLayoutSubviews 对齐)。
    fileprivate func present(data: Data) {
        guard bufW > 0, bufH > 0,
              let provider = CGDataProvider(data: data as CFData),
              let img = CGImage(width: bufW, height: bufH, bitsPerComponent: 8, bitsPerPixel: 32,
                                bytesPerRow: bufW * 4,
                                space: CGColorSpace(name: CGColorSpace.sRGB)!,
                                bitmapInfo: CGBitmapInfo(rawValue: CGImageAlphaInfo.premultipliedLast.rawValue),
                                provider: provider, decode: nil, shouldInterpolate: false,
                                intent: .defaultIntent) else {
            NSLog("gox: CGImage 创建失败")
            return
        }
        view.layer.contents = img
    }

    fileprivate func alert(_ text: String) {
        let a = UIAlertController(title: "Gox", message: text, preferredStyle: .alert)
        a.addAction(UIAlertAction(title: "好", style: .default))
        present(a, animated: true)
    }

    /// 隐藏输入框的 .editingChanged 回调: 非组合状态且有文本 = 输入法提交了一批,
    /// 整批转发并清空 (引擎侧渲染自己的光标与内容, field 只当输入管道)。
    @objc private func imeEditingChanged() {
        guard let f = imeField, f.markedTextRange == nil,
              let t = f.text, !t.isEmpty else { return }
        imeCommit(t)
        f.text = ""
    }
}

/// 隐藏输入框本体。子类化的唯一原因是拿到 `deleteBackward` —— 空框上按删除
/// 不会产生任何文本变化事件, 而引擎侧的光标回退需要知道这件事。
final class GoxField: UITextField {
    weak var target: GoxViewController?

    override func deleteBackward() {
        // 组合中 (拼音还没选定) 的退格由 super 自己改 marked text, 不转发 ——
        // 转了会把引擎侧已提交的字符误删一个。
        let composing = markedTextRange != nil
        super.deleteBackward()
        if !composing {
            target?.imeBackspace()
        }
    }
}
