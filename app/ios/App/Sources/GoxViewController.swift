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
}
