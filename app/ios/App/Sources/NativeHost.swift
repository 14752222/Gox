//
//  NativeHost.swift
//  Gox
//
//  NativeHost 契约的 iOS 实现 (六模块: device / app / geo / media / permission /
//  viewport), 对应 Go 内核 gfx/native.go 的 gfx.NativeHost。
//
//  通道协议 (与 gfx/ios/libgox/main.go 文件头的协议逐字一致, 三处同步:
//  libgox/main.go / Gox-Bridging-Header.h / 本文件):
//
//    Go 内核 Call(method, args) → args 序列化 JSON → goxCall(ctx, method, argsJson)
//      返回值三选一:
//        "__pending__"                       → NativePending (稍后 gox_resolve_native 回填)
//        {"__errCode":"…","__errMsg":"…"}    → NativeFailure (8 个统一错误码之一)
//        其它 JSON                            → NativeResult
//    回填:   gox_resolve_native(id, resultJson, errCode, errMsg) — id 照抄 args
//            里的 "__id"; errCode 非空即失败。任意线程可调 (Go 侧 gfx.Post)。
//
//  ## 线程纪律
//
//    goxCall 发生在 **Go 的脚本线程**。同步方法凡是碰 UIKit 的 (亮度/屏幕常亮/
//    设置页) 用 onMain { … } (DispatchQueue.main.sync) 完成 —— 主线程从不
//    同步等待脚本线程 (gox_tick 只叫醒泵就返回), 不会死锁。
//    异步方法 (相机/相册/定位/权限弹窗) 返回 "__pending__", 在系统回调里回填。
//
//  ## 降级口径 (绝不返回假数据)
//
//    权限没给 → permission-denied; 用户取消 → cancelled; 系统给不出的量
//    (如通知权限的同步状态) 不编造, getSetting 里缺哪个键内核就按 unknown 算;
//    gcj02 坐标给不出 → platform-error (gfx/native_geo.go: 换错比不换更糟)。
//

import AVFoundation
import AudioToolbox
import Contacts
import CoreBluetooth
import CoreLocation
import EventKit
import Photos
import PhotosUI
import QuickLook
import UIKit
import UserNotifications

final class GoxNativeHost: NSObject {

    // MARK: - 注册 (由 GoxViewController 在 gox_init 成功后调)

    /// 展示系统 UI 用的宿主视图控制器 (弱引用, GoxViewController 生命周期对齐)。
    private weak var presenter: UIViewController?

    /// 注册进 Go 内核 (能力回调 + Call 回调都是无捕获 C 闭包 + ctx 还原 self)。
    static func start(presenter: UIViewController) {
        shared.presenter = presenter
        let ctx = Unmanaged.passRetained(shared).toOpaque() // 终身持有 (进程级单例)
        gox_set_native_host(
            ctx, unsafeBitCast(onCapabilities, to: UnsafeMutableRawPointer.self),
            ctx, unsafeBitCast(onCall, to: UnsafeMutableRawPointer.self))
        shared.startReporters()
    }

    /// 能力声明表: 与 dispatch 的分支一一对应 (少报 canIUse 说"没有", 多报说谎)。
    private static let capabilityList: [String] = [
        "device.info", "device.vibrate", "device.brightness",
        "device.keepScreenOn", "device.openSettings",
        "app.share", "app.exit", "app.orientation",
        "permission.get", "permission.request",
        "camera.takePhoto", "gallery.pick", "gallery.pickVideo",
        "media.save", "media.preview",
        "location.get", "location.watch", "location.unwatch",
        // 纯上报型 (只有 Report, 没有 Call):
        "insets", "battery", "network", "appstate", "memory",
    ]

    // MARK: - C 回调 (无捕获 @convention(c), 与 flush/finished 同一模式)

    private static let onCapabilities: @convention(c) (UnsafeMutableRawPointer?) -> UnsafeMutablePointer<CChar>? = { _ in
        strdup(capabilityList.joined(separator: ","))
    }

    private static let onCall: @convention(c) (UnsafeMutableRawPointer?, UnsafePointer<CChar>?, UnsafePointer<CChar>?) -> UnsafeMutablePointer<CChar>? = { _, m, a in
        let method = m.map { String(cString: $0) } ?? ""
        let argsJson = a.map { String(cString: $0) } ?? "{}"
        let out = GoxNativeHost.shared.dispatch(method: method, argsJson: argsJson)
        return strdup(out) // Go 侧 C.free
    }

    private static let shared = GoxNativeHost()

    private override init() {
        super.init()
    }

    // MARK: - 分发 (Go 脚本线程调入)

    private func dispatch(method: String, argsJson: String) -> String {
        let args = (try? JSONSerialization.jsonObject(with: Data(argsJson.utf8)) as? [String: Any]) ?? [:]
        let id = args["__id"] as? String ?? ""
        switch method {
        // ── gx/device ──
        case "device.info": return deviceInfo()
        case "device.vibrate": return vibrate()
        case "device.brightness": return brightness(args)
        case "device.keepScreenOn": return keepScreenOn(args)
        case "device.openSettings": return openSettings()
        // ── gx/app ──
        case "app.share": return share(args, id: id)
        case "app.exit": return errJSON(GoxNativeHost.ERR_UNSUPPORTED, "iOS 不允许应用自杀 (exitApp 不可用)")
        case "app.orientation": return setOrientation(args)
        // ── gx/permission ──
        case "permission.get": return permissionGet()
        case "permission.request": return permissionRequest(args, id: id)
        // ── gx/media ──
        case "camera.takePhoto": return takePhoto(args, id: id)
        case "gallery.pick": return pickMedia(args, id: id, videos: false)
        case "gallery.pickVideo": return pickMedia(args, id: id, videos: true)
        case "media.save": return saveMedia(args, id: id)
        case "media.preview": return preview(args, id: id)
        // ── gx/geo ──
        case "location.get": return locationGet(args, id: id)
        case "location.watch": return locationWatch(args, id: id)
        case "location.unwatch": return locationUnwatch(id: id)
        // ── 未知方法: 显式 unsupported ──
        default: return errJSON(GoxNativeHost.ERR_UNSUPPORTED, "iOS 宿主未实现方法 \(method)")
        }
    }

    // MARK: - 协议助手

    // "稍后回填" 哨兵 (协议见文件头)。
    static let PENDING_MARKER = "__pending__"

    // 8 个统一错误码 (与 gfx/native.go 逐字一致)。
    static let ERR_UNSUPPORTED = "unsupported"
    static let ERR_PERMISSION_DENIED = "permission-denied"
    static let ERR_CANCELLED = "cancelled"
    static let ERR_TIMEOUT = "timeout"
    static let ERR_BUSY = "busy"
    static let ERR_UNAVAILABLE = "unavailable"
    static let ERR_PLATFORM = "platform-error"
    static let ERR_INVALID_ARG = "invalid-arg"

    private func errJSON(_ code: String, _ msg: String) -> String {
        let d: [String: String] = ["__errCode": code, "__errMsg": msg]
        if let data = try? JSONSerialization.data(withJSONObject: d),
           let s = String(data: data, encoding: .utf8) {
            return s
        }
        return "{\"__errCode\":\"\(code)\",\"__errMsg\":\"\"}"
    }

    // libgox.h 的导出签名是 char* (非 const): Swift 里 String 不能自动桥接成
    // 可变指针, 所以 strdup 进去用完 free (strdup/free 与 Go 的 C 分配器同一个
    // libc 堆, 跨界安全)。
    private func goxResolve(_ id: String, _ result: String, _ code: String, _ msg: String) {
        let a = strdup(id)!
        let b = strdup(result)!
        let c = strdup(code)!
        let d = strdup(msg)!
        gox_resolve_native(a, b, c, d)
        free(a); free(b); free(c); free(d)
    }

    private func goxReport1(_ fn: (UnsafeMutablePointer<CChar>?) -> Void, _ s: String) {
        let p = strdup(s)!
        fn(p)
        free(p)
    }

    private func goxReport2(_ fn: (UnsafeMutablePointer<CChar>?, UnsafeMutablePointer<CChar>?) -> Void, _ a: String, _ b: String) {
        let p = strdup(a)!
        let q = strdup(b)!
        fn(p, q)
        free(p); free(q)
    }

    private func resolve(_ id: String, _ result: [String: Any]) {
        guard !id.isEmpty else { return }
        guard let data = try? JSONSerialization.data(withJSONObject: result),
              let s = String(data: data, encoding: .utf8) else {
            fail(id, GoxNativeHost.ERR_PLATFORM, "结果序列化失败")
            return
        }
        goxResolve(id, s, "", "")
    }

    private func resolve(_ id: String, _ result: [[String: Any]]) {
        guard !id.isEmpty else { return }
        guard let data = try? JSONSerialization.data(withJSONObject: result),
              let s = String(data: data, encoding: .utf8) else {
            fail(id, GoxNativeHost.ERR_PLATFORM, "结果序列化失败")
            return
        }
        goxResolve(id, s, "", "")
    }

    private func fail(_ id: String, _ code: String, _ msg: String) {
        guard !id.isEmpty else { return }
        goxResolve(id, "", code, msg)
    }

    /// 在主线程同步执行 (读 UIKit 状态用)。主线程从不阻塞等 Go 线程, 安全。
    @discardableResult
    private func onMain<T>(_ work: @escaping () -> T) -> T {
        if Thread.isMainThread { return work() }
        return DispatchQueue.main.sync { work() }
    }

    private func str(_ args: [String: Any], _ key: String) -> String { args[key] as? String ?? "" }
    private func num(_ args: [String: Any], _ key: String) -> Double { (args[key] as? NSNumber)?.doubleValue ?? 0 }
    private func bool(_ args: [String: Any], _ key: String, _ def: Bool) -> Bool { (args[key] as? NSNumber)?.boolValue ?? def }

    // MARK: - gx/device

    private func deviceInfo() -> String {
        let device = onMain { UIDevice.current }
        let locale = Locale.current
        let tz = TimeZone.current
        let bundle = Bundle.main
        let osVersion = device.systemVersion
        let sdk = Int(osVersion.split(separator: ".").first ?? "0") ?? 0
        var screenW = 0, screenH = 0, scale = 1.0
        onMain {
            let s = UIScreen.main
            scale = Double(s.scale)
            screenW = Int(s.bounds.width * s.scale)
            screenH = Int(s.bounds.height * s.scale)
        }
        #if targetEnvironment(simulator)
        let isEmulator = true
        #else
        let isEmulator = false
        #endif
        let info: [String: Any] = [
            "platform": "ios",
            "os": "iOS \(osVersion)",
            "osVersion": osVersion,
            "arch": "arm64",
            "model": device.model,
            "brand": "apple",
            "manufacturer": "apple",
            "deviceId": device.identifierForVendor?.uuidString ?? "",
            "locale": locale.identifier,
            "language": locale.languageCode ?? "",
            "region": locale.regionCode ?? "",
            "timezone": tz.identifier,
            "tzOffset": tz.secondsFromGMT() / 60,
            "sdkVersion": sdk,
            "screenWidth": screenW,
            "screenHeight": screenH,
            "pixelRatio": scale,
            "isEmulator": isEmulator,
            // 大屏判据与内核缺省 (宽>=900 且移动端) 同语义: iPad 算 tablet。
            "isTablet": device.userInterfaceIdiom == .pad,
            "appName": (bundle.object(forInfoDictionaryKey: "CFBundleDisplayName") as? String)
                ?? (bundle.object(forInfoDictionaryKey: "CFBundleName") as? String) ?? "Gox",
            "appVersion": (bundle.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String) ?? "",
            "appBuild": (bundle.object(forInfoDictionaryKey: "CFBundleVersion") as? String) ?? "",
        ]
        return jsonStr(info)
    }

    /// device.vibrate: iOS 只有一档震动 (系统级), duration/quality 不细分。
    private func vibrate() -> String {
        AudioServicesPlaySystemSound(kSystemSoundID_Vibrate)
        return "true"
    }

    /// device.brightness: 带 value 设置 (0..1), 不带读取。读不出 → -1 (不猜)。
    private func brightness(_ args: [String: Any]) -> String {
        if let v = args["value"] as? NSNumber {
            let clamped = min(max(v.doubleValue, 0), 1)
            onMain { UIScreen.main.brightness = CGFloat(clamped) }
            return jsonStr(["value": clamped])
        }
        let cur = onMain { Double(UIScreen.main.brightness) }
        return jsonStr(["value": cur])
    }

    private func keepScreenOn(_ args: [String: Any]) -> String {
        let on = bool(args, "on", true)
        onMain { UIApplication.shared.isIdleTimerDisabled = on }
        return "true"
    }

    private func openSettings() -> String {
        let ok = onMain { () -> Bool in
            guard let url = URL(string: UIApplication.openSettingsURLString) else { return false }
            UIApplication.shared.open(url, options: [:], completionHandler: nil)
            return true
        }
        return ok ? "true" : errJSON(GoxNativeHost.ERR_PLATFORM, "打开设置页失败")
    }

    // MARK: - gx/app

    /// 系统分享 (UIActivityViewController)。分享面板没有结果回调, 拉起即 resolve {}。
    private func share(_ args: [String: Any], id: String) -> String {
        let text = str(args, "text")
        let url = str(args, "url")
        let imagePath = str(args, "imagePath")
        let files = args["files"] as? [[String: Any]] ?? []
        if text.isEmpty && url.isEmpty && imagePath.isEmpty && files.isEmpty {
            return errJSON(GoxNativeHost.ERR_INVALID_ARG, "share: 至少要给 text / url / imagePath / files 之一")
        }
        DispatchQueue.main.async {
            guard let vc = self.presenter else {
                self.fail(id, GoxNativeHost.ERR_PLATFORM, "没有可用的窗口来展示分享面板")
                return
            }
            var body = text
            if !url.isEmpty { body = body.isEmpty ? url : body + " " + url }
            var items: [Any] = []
            if !body.isEmpty { items.append(body) }
            for path in ([imagePath] + files.compactMap { ($0["uri"] as? String) ?? ($0["path"] as? String) })
                .filter({ !$0.isEmpty }) {
                if let fileURL = Self.materialize(path) { items.append(fileURL) }
            }
            let av = UIActivityViewController(activityItems: items, applicationActivities: nil)
            av.popoverPresentationController?.sourceView = vc.view // iPad 必需
            vc.present(av, animated: true)
            self.resolve(id, [:])
        }
        return Self.PENDING_MARKER
    }

    /// app.orientation: 动态改 supportedInterfaceOrientationsFor 的返回
    /// (AppDelegate 读 GoxNativeHost.orientationMask)。注意 Info.plist 的
    /// UISupportedInterfaceOrientations 必须包含请求的方向, 否则系统不理。
    private func setOrientation(_ args: [String: Any]) -> String {
        let mode = (str(args, "mode") as String).lowercased()
        let mask: UIInterfaceOrientationMask
        switch mode {
        case "portrait": mask = [.portrait]
        case "landscape": mask = [.landscapeLeft, .landscapeRight]
        case "auto": mask = [.portrait, .landscapeLeft, .landscapeRight]
        default: return errJSON(GoxNativeHost.ERR_INVALID_ARG, "setOrientation: 不认识的模式 \(mode)")
        }
        GoxNativeHost.orientationMask = mask
        onMain { UIViewController.attemptRotationToDeviceOrientation() }
        return "true"
    }

    static var orientationMask: UIInterfaceOrientationMask = .portrait

    // MARK: - gx/permission

    /// permission.get → {location, camera, gallery, microphone, contacts,
    /// calendar, bluetooth, storage}。
    /// notification 的同步状态 iOS 拿不到 (UNUserNotificationCenter 只有异步
    /// API) —— 诚实缺键, 内核按 unknown 算, 绝不编造。
    private func permissionGet() -> String {
        var o: [String: String] = [:]
        o["location"] = permWord(CLLocationManager().authorizationStatus)
        o["camera"] = permWord(AVCaptureDevice.authorizationStatus(for: .video))
        o["gallery"] = galleryWord(PHPhotoLibrary.authorizationStatus(for: .readWrite))
        o["microphone"] = permWord(AVCaptureDevice.authorizationStatus(for: .audio))
        o["contacts"] = permWord(CNContactStore.authorizationStatus(for: .contacts))
        o["calendar"] = permWord(EKEventStore.authorizationStatus(for: .event))
        // CoreBluetooth 的 authorization (iOS 13+): allowed* → granted。
        switch CBManager.authorization {
        case .allowedAlways: o["bluetooth"] = "granted"
        case .denied: o["bluetooth"] = "denied"
        case .restricted: o["bluetooth"] = "restricted"
        case .notDetermined: o["bluetooth"] = "not-determined"
        @unknown default: o["bluetooth"] = "unknown"
        }
        // iOS 没有"存储"运行时权限 (应用沙盒天然可写) → granted 是事实而非假设。
        o["storage"] = "granted"
        return jsonStr(o)
    }

    private func permWord(_ s: AVAuthorizationStatus) -> String {
        switch s {
        case .authorized: return "granted"
        case .denied: return "denied"
        case .notDetermined: return "not-determined"
        case .restricted: return "restricted"
        @unknown default: return "unknown"
        }
    }

    private func permWord(_ s: CLAuthorizationStatus) -> String {
        switch s {
        case .authorizedAlways, .authorizedWhenInUse: return "granted"
        case .denied: return "denied"
        case .notDetermined: return "not-determined"
        case .restricted: return "restricted"
        @unknown default: return "unknown"
        }
    }

    private func permWord<T>(_ s: T) -> String {
        // CNAuthorizationStatus / EKAuthorizationStatus 的通用映射 (authorized 组都算 granted)。
        let v: Int
        if let n = s as? Int { v = n } else { return "unknown" }
        // 两套枚举数值不同, 用原始值兜底: 0 = notDetermined, 1 = restricted,
        // 2 = denied, 3 = authorized (两套一致)。
        switch v {
        case 0: return "not-determined"
        case 1: return "restricted"
        case 2: return "denied"
        case 3: return "granted"
        default: return "unknown"
        }
    }

    private func galleryWord(_ s: PHAuthorizationStatus) -> String {
        switch s {
        case .authorized: return "granted"
        case .limited: return "limited" // iOS 的"仅选中的照片", 内核有独立取值
        case .denied: return "denied"
        case .notDetermined: return "not-determined"
        case .restricted: return "restricted"
        @unknown default: return "unknown"
        }
    }

    /// permission.request {kind} → Promise<状态词>。弹系统弹窗, 回调里回填。
    private func permissionRequest(_ args: [String: Any], id: String) -> String {
        let kind = str(args, "kind")
        switch kind {
        case "location":
            ensureLocationAuth { state in
                self.resolve(id, ["state": state])
            }
        case "camera":
            AVCaptureDevice.requestAccess(for: .video) { granted in
                self.resolve(id, ["state": granted ? "granted" : "denied"])
            }
        case "microphone":
            AVCaptureDevice.requestAccess(for: .audio) { granted in
                self.resolve(id, ["state": granted ? "granted" : "denied"])
            }
        case "gallery":
            PHPhotoLibrary.requestAuthorization(for: .readWrite) { status in
                self.resolve(id, ["state": self.galleryWord(status)])
            }
        case "notification":
            UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .badge, .sound]) { granted, _ in
                self.resolve(id, ["state": granted ? "granted" : "denied"])
            }
        case "contacts":
            let store = CNContactStore()
            store.requestAccess(for: .contacts) { granted, _ in
                self.resolve(id, ["state": granted ? "granted" : "denied"])
            }
        case "calendar":
            let store = EKEventStore()
            // iOS 17+ 用 requestFullAccessToEvents; 旧 API 兼容写法。
            if #available(iOS 17.0, *) {
                store.requestFullAccessToEvents { granted, _ in
                    self.resolve(id, ["state": granted ? "granted" : "denied"])
                }
            } else {
                store.requestAccess(to: .event) { granted, _ in
                    self.resolve(id, ["state": granted ? "granted" : "denied"])
                }
            }
        default:
            return errJSON(GoxNativeHost.ERR_UNSUPPORTED, "iOS 未实现权限 \(kind) 的申请")
        }
        return Self.PENDING_MARKER
    }

    // MARK: - gx/media

    /// camera.takePhoto {camera:"back"|"front", quality:0..100, …}。
    private func takePhoto(_ args: [String: Any], id: String) -> String {
        guard AVCaptureDevice.authorizationStatus(for: .video) == .authorized else {
            // 没授权: not-determined 的场合内核应用会先 authorize; 这里直接报
            // permission-denied, 不替脚本偷偷弹窗。
            fail(id, GoxNativeHost.ERR_PERMISSION_DENIED, "相机权限未授权 (先调 authorize(\"camera\"))")
            return Self.PENDING_MARKER
        }
        DispatchQueue.main.async {
            guard let vc = self.presenter else {
                self.fail(id, GoxNativeHost.ERR_PLATFORM, "没有可用的窗口来展示相机")
                return
            }
            let picker = UIImagePickerController()
            picker.sourceType = .camera
            picker.cameraDevice = self.str(args, "camera") == "front" ? .front : .rear
            picker.delegate = self
            self.cameraID = id
            let quality = min(max(self.num(args, "quality") == 0 ? 90 : self.num(args, "quality"), 1), 100)
            self.cameraQuality = quality
            vc.present(picker, animated: true)
        }
        return Self.PENDING_MARKER
    }

    private var cameraID = ""
    private var cameraQuality = 90.0

    /// gallery.pick {count, …} / gallery.pickVideo → PHPicker (系统相册选择器,
    /// 进程外运行, 不需要相册权限)。
    private func pickMedia(_ args: [String: Any], id: String, videos: Bool) -> String {
        DispatchQueue.main.async {
            guard let vc = self.presenter else {
                self.fail(id, GoxNativeHost.ERR_PLATFORM, "没有可用的窗口来展示相册")
                return
            }
            var config = PHPickerConfiguration()
            config.filter = videos ? .videos : .images
            let count = videos ? 1 : min(max(Int(self.num(args, "count")) == 0 ? 1 : Int(self.num(args, "count")), 1), 9)
            config.selectionLimit = count
            let picker = PHPickerViewController(configuration: config)
            picker.delegate = self
            if videos { self.videoID = id } else { self.galleryID = id; self.galleryCount = count }
            vc.present(picker, animated: true)
        }
        return Self.PENDING_MARKER
    }

    private var galleryID = ""
    private var galleryCount = 1
    private var videoID = ""

    /// media.save {album, file:{path/uri}} → 写入系统相册 (add-only 权限)。
    private func saveMedia(_ args: [String: Any], id: String) -> String {
        let file = args["file"] as? [String: Any] ?? [:]
        let src = (file["uri"] as? String) ?? (file["path"] as? String) ?? ""
        if src.isEmpty {
            return errJSON(GoxNativeHost.ERR_INVALID_ARG, "media.save: 需要含 path/uri 的 file")
        }
        guard let image = UIImage(contentsOfFile: Self.materializePath(src)) ?? (src.hasPrefix("file://") ? UIImage(contentsOfFile: String(src.dropFirst(7))) : nil) else {
            return errJSON(GoxNativeHost.ERR_UNAVAILABLE, "media.save: 文件读不出来 (\(src))")
        }
        switch PHPhotoLibrary.authorizationStatus(for: .addOnly) {
        case .authorized, .limited: break
        case .denied, .restricted:
            return errJSON(GoxNativeHost.ERR_PERMISSION_DENIED, "保存到相册需要\"添加照片\"权限")
        case .notDetermined:
            PHPhotoLibrary.requestAuthorization(for: .addOnly) { _ in }
            return errJSON(GoxNativeHost.ERR_PERMISSION_DENIED, "相册权限尚未授权, 重试一次")
        @unknown default:
            return errJSON(GoxNativeHost.ERR_PLATFORM, "相册权限状态未知")
        }
        PHPhotoLibrary.shared().performChanges {
            PHAssetChangeRequest.creationRequestForAsset(from: image)
        } completionHandler: { ok, error in
            if ok {
                self.resolve(id, ["path": src, "uri": src])
            } else {
                self.fail(id, GoxNativeHost.ERR_PLATFORM, "保存失败: \(error?.localizedDescription ?? "未知错误")")
            }
        }
        return Self.PENDING_MARKER
    }

    /// media.preview {files, index} → QLPreviewController (系统看图/看视频)。
    /// 列表里所有可访问的文件都交给 QL (可左右滑动切换), 从 index 开始;
    /// 个别文件失效只跳过 (起点按失效数修正), 全部失效才报错。
    private func preview(_ args: [String: Any], id: String) -> String {
        let files = args["files"] as? [[String: Any]] ?? []
        if files.isEmpty {
            return errJSON(GoxNativeHost.ERR_INVALID_ARG, "media.preview: 文件列表为空")
        }
        let idx = min(max(Int(num(args, "index")), 0), files.count - 1)
        var urls: [URL] = []
        var missingBefore = 0
        for (i, f) in files.enumerated() {
            let src = (f["uri"] as? String) ?? (f["path"] as? String) ?? ""
            if let fileURL = Self.materialize(src) {
                urls.append(fileURL)
            } else if i < idx {
                missingBefore += 1
            }
        }
        if urls.isEmpty {
            return errJSON(GoxNativeHost.ERR_UNAVAILABLE, "文件不存在或无法访问")
        }
        let startIdx = max(0, min(idx - missingBefore, urls.count - 1))
        DispatchQueue.main.async {
            guard let vc = self.presenter else {
                self.fail(id, GoxNativeHost.ERR_PLATFORM, "没有可用的窗口来展示预览")
                return
            }
            let ql = QLPreviewController()
            ql.dataSource = self
            self.previewURLs = urls
            self.previewID = id
            ql.currentPreviewItemIndex = startIdx
            // iPad 形态固定为 pageSheet (automatic 在 iPad 上的收折行为随容器
            // 变化; 显式 pageSheet 保证真机/iPad/浮窗下行为一致)。
            ql.modalPresentationStyle = .pageSheet
            vc.present(ql, animated: true)
            // 预览没有可靠的"看完了"回调, 拉起即 resolve (与 share 同口径)。
            self.resolve(id, [:])
        }
        return Self.PENDING_MARKER
    }

    private var previewID = ""
    private var previewURLs: [URL] = []

    /// 把脚本给的路径/URI 变成可读的本地 file URL (content 之外的都落 tmp)。
    private static func materialize(_ src: String) -> URL? {
        if src.hasPrefix("file://"), let u = URL(string: src) {
            return FileManager.default.fileExists(atPath: u.path) ? u : nil
        }
        let p = materializePath(src)
        return p.isEmpty ? nil : URL(fileURLWithPath: p)
    }

    private static func materializePath(_ src: String) -> String {
        if src.hasPrefix("/") { return FileManager.default.fileExists(atPath: src) ? src : "" }
        return "" // content:// 等平台标识 iOS 上不出现; 给不出就诚实返回空
    }

    /// MediaFile 结果 JSON (字段与 gfx/native_media.go 对齐)。
    private func mediaJSON(path: String, mime: String, size: Int = -1) -> [String: Any] {
        let name = (path as NSString).lastPathComponent
        return [
            "path": path,
            "uri": path,
            "name": name,
            "mimeType": mime,
            "width": 0,
            "height": 0,
            "size": size,
            "duration": -1,
            "createdAt": Int(Date().timeIntervalSince1970 * 1000),
        ]
    }

    // MARK: - gx/geo

    /// CLLocationManager 必须活在有 runloop 的线程上 —— 脚本线程不是, 所以
    /// 创建/调用一律经 onMain 收口 (回调也因此在主线程)。
    private var _locationManager: CLLocationManager?
    private var locationManager: CLLocationManager {
        onMain {
            if let m = self._locationManager { return m }
            let m = CLLocationManager()
            m.delegate = self
            self._locationManager = m
            return m
        }
    }

    /// 活跃的 watch: __id → true (location.watch 共用一个 CLLocationManager)。
    private var watches: Set<String> = []
    private var oneShotID = ""

    private func locationGet(_ args: [String: Any], id: String) -> String {
        if str(args, "type") == "gcj02" {
            return errJSON(GoxNativeHost.ERR_PLATFORM, "location.get: iOS 无法提供 gcj02 坐标 (只支持 wgs84)")
        }
        ensureLocationAuth { [self] state in
            guard state == "granted" else {
                fail(id, GoxNativeHost.ERR_PERMISSION_DENIED, "定位权限未授权 (\(state))")
                return
            }
            oneShotID = id
            onMain {
                locationManager.desiredAccuracy = bool(args, "highAccuracy", false)
                    ? kCLLocationAccuracyBest : kCLLocationAccuracyHundredMeters
                locationManager.startUpdatingLocation()
            }
        }
        return Self.PENDING_MARKER
    }

    private func locationWatch(_ args: [String: Any], id: String) -> String {
        if str(args, "type") == "gcj02" {
            return errJSON(GoxNativeHost.ERR_PLATFORM, "location.watch: iOS 无法提供 gcj02 坐标 (只支持 wgs84)")
        }
        ensureLocationAuth { [self] state in
            guard state == "granted" else {
                fail(id, GoxNativeHost.ERR_PERMISSION_DENIED, "定位权限未授权 (\(state))")
                return
            }
            watches.insert(id)
            onMain {
                locationManager.desiredAccuracy = bool(args, "highAccuracy", false)
                    ? kCLLocationAccuracyBest : kCLLocationAccuracyHundredMeters
                locationManager.startUpdatingLocation()
            }
        }
        return Self.PENDING_MARKER
    }

    private func locationUnwatch(id: String) -> String {
        watches.remove(id)
        if oneShotID.isEmpty && watches.isEmpty {
            onMain { self.locationManager.stopUpdatingLocation() }
        }
        return "true"
    }

    /// 授权流程: not-determined 先弹窗, 结果经回调继续 (denied/restricted 直接给词)。
    private func ensureLocationAuth(_ done: @escaping (String) -> Void) {
        let status = CLLocationManager().authorizationStatus
        switch status {
        case .authorizedAlways, .authorizedWhenInUse:
            done("granted")
        case .denied, .restricted:
            done(status == .restricted ? "restricted" : "denied")
        case .notDetermined:
            pendingLocationAuth = done
            DispatchQueue.main.async { self.locationManager.requestWhenInUseAuthorization() }
        @unknown default:
            done("unknown")
        }
    }

    private var pendingLocationAuth: ((String) -> Void)?

    /// 定位 → 回填 JSON (字段与 gfx/native_geo.go locationFromJS 对齐)。
    private func locationJSON(_ loc: CLLocation) -> [String: Any] {
        return [
            "latitude": loc.coordinate.latitude,
            "longitude": loc.coordinate.longitude,
            "altitude": loc.altitude,
            "accuracy": loc.horizontalAccuracy,
            "altitudeAccuracy": loc.verticalAccuracy,
            "speed": max(0, loc.speed),
            "heading": max(0, loc.course),
            "timestamp": Int(loc.timestamp.timeIntervalSince1970 * 1000),
            "provider": "fused",
            "mocked": false,
            "type": "wgs84", // iOS 系统定位即 WGS84, 不做火星换算
        ]
    }

    // MARK: - 上报通道 (battery / network / appstate / memory)

    private func startReporters() {
        // 电池: 开监听 + 立即报一次初值 (useBattery() 启动即有真值)。
        onMain {
            UIDevice.current.isBatteryMonitoringEnabled = true
        }
        NotificationCenter.default.addObserver(forName: UIDevice.batteryStateDidChangeNotification,
                                               object: nil, queue: nil) { [weak self] _ in
            self?.reportBattery()
        }
        NotificationCenter.default.addObserver(forName: UIDevice.batteryLevelDidChangeNotification,
                                               object: nil, queue: nil) { [weak self] _ in
            self?.reportBattery()
        }
        reportBattery()

        // 网络: NWPathMonitor (Network 框架, 自带队列)。
        let monitor = NWPathMonitor()
        monitor.pathUpdateHandler = { [weak self] path in
            let connected = path.status == .satisfied
            var type = "other"
            if !connected {
                type = "none"
            } else if path.usesInterfaceType(.wifi) {
                type = "wifi"
            } else if path.usesInterfaceType(.cellular) {
                type = "cellular"
            } else if path.usesInterfaceType(.wiredEthernet) {
                type = "ethernet"
            }
            let o: [String: Any] = [
                "connected": connected,
                "type": type,
                "metered": path.isConstrained,
                "strength": -1,
            ]
            self?.reportNetworkJSON(o)
        }
        monitor.start(queue: DispatchQueue(label: "gox.network.monitor"))
        self.networkMonitor = monitor

        // 前后台: 宿主上报 (gx/native_app.go 的分工)。
        let center = NotificationCenter.default
        center.addObserver(forName: UIApplication.didBecomeActiveNotification, object: nil, queue: nil) { _ in
            self.goxReport1(gox_report_app_state, "active")
        }
        center.addObserver(forName: UIApplication.willResignActiveNotification, object: nil, queue: nil) { _ in
            self.goxReport1(gox_report_app_state, "inactive")
        }
        center.addObserver(forName: UIApplication.didEnterBackgroundNotification, object: nil, queue: nil) { _ in
            self.goxReport1(gox_report_app_state, "background")
        }
        // 内存警告。
        center.addObserver(forName: UIApplication.didReceiveMemoryWarningNotification, object: nil, queue: nil) { _ in
            gox_report_memory_warning()
        }
        // 权限状态初值 (启动时批量报一遍; notification 拿不到同步值就不报)。
        goxReport2(gox_report_permission, "location", permWord(CLLocationManager().authorizationStatus))
        goxReport2(gox_report_permission, "camera", permWord(AVCaptureDevice.authorizationStatus(for: .video)))
        goxReport2(gox_report_permission, "gallery", galleryWord(PHPhotoLibrary.authorizationStatus(for: .readWrite)))
        goxReport2(gox_report_permission, "microphone", permWord(AVCaptureDevice.authorizationStatus(for: .audio)))
    }

    private var networkMonitor: NWPathMonitor?

    private func reportBattery() {
        let d = onMain { () -> [String: Any] in
            let dev = UIDevice.current
            let state = dev.batteryState
            let level = dev.batteryLevel // 0..1; -1 = 未知
            let chargingType: String
            switch state {
            case .charging: chargingType = "unknown" // iOS 不区分 ac/usb/wireless, 不猜
            case .full: chargingType = "ac"
            case .unplugged: chargingType = "none"
            case .unknown: chargingType = "unknown"
            @unknown default: chargingType = "unknown"
            }
            return [
                "supported": state != .unknown && dev.batteryLevel >= 0,
                "level": level >= 0 ? level : -1,
                "charging": state == .charging,
                "chargingType": chargingType,
                "temperature": -1, // iOS 不公开电池温度, 诚实报未知
                "lowPowerMode": ProcessInfo.processInfo.isLowPowerModeEnabled,
            ]
        }
        goxReport1(gox_report_battery, jsonStr(d))
    }

    /// NWPathMonitor 回调里的网络上报 (字段与 gfx.NetworkState 对齐)。
    private func reportNetworkJSON(_ o: [String: Any]) {
        goxReport1(gox_report_network, jsonStr(o))
    }

    private func jsonStr(_ o: [String: Any]) -> String {
        guard let data = try? JSONSerialization.data(withJSONObject: o),
              let s = String(data: data, encoding: .utf8) else { return "{}" }
        return s
    }
}

// MARK: - PHPickerViewControllerDelegate (相册选择)

extension GoxNativeHost: PHPickerViewControllerDelegate {

    func picker(_ picker: PHPickerViewController, didFinishPicking results: [PHPickerResult]) {
        let id = galleryID
        let videoID = self.videoID
        galleryID = ""
        self.videoID = ""
        picker.dismiss(animated: true)

        if results.isEmpty {
            // 用户取消/没选 → cancelled (不是失败, 也不是假成功)。
            if !id.isEmpty { fail(id, GoxNativeHost.ERR_CANCELLED, "用户取消了选择") }
            if !videoID.isEmpty { fail(videoID, GoxNativeHost.ERR_CANCELLED, "用户取消了选择") }
            return
        }
        // 逐个把 item provider 的字节落成 tmp 文件, 全部完成后一次回填数组。
        // 结果数组按下标预分配、回调里按下标写入 —— provider 完成顺序不定,
        // 用 append 会导致多选回填顺序与用户选择顺序不一致。
        let group = DispatchGroup()
        var slots = [[String: Any]?](repeating: nil, count: results.count)
        var firstError = ""
        let lock = NSLock()
        for (idx, r) in results.enumerated() {
            let provider = r.itemProvider
            let isVideo = provider.hasItemConformingToTypeIdentifier("public.movie")
            group.enter()
            let type = isVideo ? "public.movie" : "public.image"
            provider.loadDataRepresentation(forTypeIdentifier: type) { data, error in
                defer { group.leave() }
                guard let data = data, !data.isEmpty else {
                    lock.lock()
                    if firstError.isEmpty { firstError = error?.localizedDescription ?? "数据为空" }
                    lock.unlock()
                    return
                }
                let ext = isVideo ? "mov" : "jpg"
                let url = FileManager.default.temporaryDirectory
                    .appendingPathComponent("gox-\(UUID().uuidString).\(ext)")
                do {
                    try data.write(to: url)
                    let mime = isVideo ? "video/mp4" : "image/jpeg"
                    lock.lock()
                    slots[idx] = [
                        "path": url.path, "uri": url.path, "name": url.lastPathComponent,
                        "mimeType": mime, "size": data.count,
                        "width": 0, "height": 0, "duration": -1,
                        "createdAt": Int(Date().timeIntervalSince1970 * 1000),
                    ]
                    lock.unlock()
                } catch {
                    lock.lock()
                    if firstError.isEmpty { firstError = error.localizedDescription }
                    lock.unlock()
                }
            }
        }
        group.notify(queue: .main) { [self] in
            let files = slots.compactMap { $0 }
            if !id.isEmpty {
                if files.isEmpty {
                    fail(id, GoxNativeHost.ERR_PLATFORM, "选择器返回的内容读不出来: \(firstError)")
                } else {
                    resolve(id, files)
                }
            }
            if !videoID.isEmpty {
                if let first = files.first {
                    resolve(videoID, first)
                } else {
                    fail(videoID, GoxNativeHost.ERR_PLATFORM, "选择器返回的内容读不出来: \(firstError)")
                }
            }
        }
    }
}

// MARK: - UIImagePickerControllerDelegate (相机)

extension GoxNativeHost: UIImagePickerControllerDelegate, UINavigationControllerDelegate {

    func imagePickerController(_ picker: UIImagePickerController,
                               didFinishPickingMediaWithInfo info: [UIImagePickerController.InfoKey: Any]) {
        let id = cameraID
        cameraID = ""
        picker.dismiss(animated: true)
        guard !id.isEmpty else { return }
        guard let image = info[.originalImage] as? UIImage else {
            fail(id, GoxNativeHost.ERR_PLATFORM, "相机没有返回图像")
            return
        }
        // 落成 tmp jpeg (quality 来自 args.quality, 缺省 90 —— 与内核缺省一致)。
        guard let data = image.jpegData(compressionQuality: CGFloat(cameraQuality / 100.0)) else {
            fail(id, GoxNativeHost.ERR_PLATFORM, "照片编码失败")
            return
        }
        let url = FileManager.default.temporaryDirectory
            .appendingPathComponent("gox-cam-\(UUID().uuidString).jpg")
        do {
            try data.write(to: url)
        } catch {
            fail(id, GoxNativeHost.ERR_PLATFORM, "照片写入失败: \(error.localizedDescription)")
            return
        }
        var m = mediaJSON(path: url.path, mime: "image/jpeg", size: data.count)
        m["width"] = image.cgImage?.width ?? 0
        m["height"] = image.cgImage?.height ?? 0
        resolve(id, m)
    }

    func imagePickerControllerDidCancel(_ picker: UIImagePickerController) {
        let id = cameraID
        cameraID = ""
        picker.dismiss(animated: true)
        if !id.isEmpty { fail(id, GoxNativeHost.ERR_CANCELLED, "用户取消了拍摄") }
    }
}

// MARK: - QLPreviewControllerDataSource (系统预览)

extension GoxNativeHost: QLPreviewControllerDataSource {

    func numberOfPreviewItems(in controller: QLPreviewController) -> Int { previewURLs.count }

    func previewController(_ controller: QLPreviewController, previewItemAt index: Int) -> QLPreviewItem {
        (previewURLs[min(index, previewURLs.count - 1)] as NSURL) as QLPreviewItem
    }
}

// MARK: - CLLocationManagerDelegate (定位)

extension GoxNativeHost: CLLocationManagerDelegate {

    func locationManagerDidChangeAuthorization(_ manager: CLLocationManager) {
        guard let done = pendingLocationAuth else { return }
        switch manager.authorizationStatus {
        case .notDetermined:
            return // 还在等用户
        case .authorizedAlways, .authorizedWhenInUse:
            pendingLocationAuth = nil
            done("granted")
        case .denied:
            pendingLocationAuth = nil
            done("denied")
        case .restricted:
            pendingLocationAuth = nil
            done("restricted")
        @unknown default:
            pendingLocationAuth = nil
            done("unknown")
        }
    }

    func locationManager(_ manager: CLLocationManager, didUpdateLocations locations: [CLLocation]) {
        guard let loc = locations.last else { return }
        // 一次定位: 回填并停止。
        let shot = oneShotID
        if !shot.isEmpty {
            oneShotID = ""
            if watches.isEmpty { manager.stopUpdatingLocation() }
            resolve(shot, locationJSON(loc))
            return
        }
        // 流式 watch: 对每个活跃 id 反复回填 (内核 stream 语义, 不摘条目)。
        for id in watches {
            resolve(id, locationJSON(loc))
        }
    }

    func locationManager(_ manager: CLLocationManager, didFailWithError error: Error) {
        let clErr = error as? CLError
        // 单次失败不终止 watch (内核 nativeErrFatal: 可恢复错误让流继续)。
        // 只对一次定位报错; watch 的失败也投给 sink (err 位), 由脚本决定怎么办。
        if !oneShotID.isEmpty {
            let id = oneShotID
            oneShotID = ""
            if watches.isEmpty { manager.stopUpdatingLocation() }
            if clErr?.code == .denied {
                fail(id, GoxNativeHost.ERR_PERMISSION_DENIED, "定位权限被拒绝")
            } else {
                fail(id, GoxNativeHost.ERR_UNAVAILABLE, "定位失败: \(error.localizedDescription)")
            }
        }
    }
}
