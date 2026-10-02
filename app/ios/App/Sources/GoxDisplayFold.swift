//
//  GoxDisplayFold.swift
//  Gox
//
//  iPhone Duo (iOS 27.1) 折叠屏上报: 姿态 / 折痕 / 保留区 / 尺寸类 → libgox。
//
//  为什么单独一个文件: 这段是唯一碰 iOS 27.1 新 API (`reservedRegions`) 的地方;
//  混进 GoxViewController 会让"哪几行是新系统才有的"变得看不出来。契约的另一端
//  是 gfx/mobile.ReportDisplayFold (解析 + 校验 + 归一化) 与 gfx/ios/libgox/
//  main.go 的 `//export gox_set_display_fold`。
//
//  ── 三条纪律 (与内核注释同源, 改动前先读) ─────────────────────────────
//
//  ① **坐标一律转设备像素**: 与 reportSafeAreaInsets() 同一取舍 —— 内核侧的
//     视口/布局全在设备像素上, 点值混进去会在 Retina 上错一个 scale 倍。
//  ② **不猜**: 宿主只报"看到的", 姿态判定 (half-open / flat / folded) 与
//     折痕归一化全部交给 Go 侧 —— 那边有 `go test ./gfx/mobile` 的测试面。
//  ③ **不自己算 hasFold**: 结构性折痕的判据在 gfx/mobile.StructuralRegions。
//     这里只负责把 `kind` 如实填上 (division / occlusion)。
//
//  ── 为什么走运行时动态派发 (重要, 踩过坑) ─────────────────────────────
//
//  `view.reservedRegions(kind:options:)` 的 Apple 文档标 iOS 27.1+, 但写这版
//  代码时的 SDK (27.0) 里**根本没有这个符号** —— 直接写编译期调用连编译都过
//  不去 (unrecognized member, 已实测)。所以这里用 NSSelectorFromString +
//  IMP 转发: selector 存在 (真机 27.1+) 就走, 不存在就整条 27.1 分支跳过,
//  只报 sizeClass/几何这些**今天就能拿到的真实数据** —— 绝不崩、绝不猜姿态。
//  等 SDK 带上符号后, 这里可以换回编译期调用 (把 dynamicFoldPayload 里的
//  假设值核对一遍再换)。
//

import UIKit

extension GoxViewController {

    /// 上报当前折叠状态。可重复调 (内核侧是 upsert, 幂等)。
    ///
    /// 调用时机 (三处 + 一次补报, 缺一会漏):
    ///   - `viewWillTransition(to:)`      旋转 / 进分屏 (几何变)
    ///   - `traitCollectionDidChange`     尺寸类变 (几何**可能不变**)
    ///   - `viewSafeAreaInsetsDidChange`  安全区变 (见下)
    ///   - `startEngine` 成功后补报一次    首次布局早于 gox_init 时被吞掉的那一次
    func reportDisplayFold() {
        guard inited else { return }

        let scale = renderScale
        let w = Int(view.bounds.width * scale)
        let h = Int(view.bounds.height * scale)

        var payload: [String: Any] = [
            "width": w,
            "height": h,
            "sizeClass": [
                "width": sizeClassName(traitCollection.horizontalSizeClass),
                "height": sizeClassName(traitCollection.verticalSizeClass),
            ],
        ]

        // 27.1 分支: selector 存在才走 (动态派发, 见文件头说明)。
        if #available(iOS 27.1, *),
           let fold = Self.dynamicFoldPayload(in: view, scale: scale) {
            for (k, v) in fold {
                payload[k] = v
            }
        }

        guard let data = try? JSONSerialization.data(withJSONObject: payload),
              let json = String(data: data, encoding: .utf8) else {
            NSLog("gox: 折叠上报 JSON 序列化失败")
            return
        }
        json.withCString { p in
            gox_set_display_fold(UnsafeMutablePointer(mutating: p))
        }
    }

    /// UIKit 的尺寸类 → 脚本侧词汇 (与 gfx/viewport.go 的 SizeCompact/SizeRegular 对齐)。
    ///
    /// `unspecified` 映射成空串而不是 "regular": 内核侧空串 = "这次没带这个字段",
    /// 会保留上一次的值; 而谎报一个 regular 会把真实的 compact 覆盖掉。
    private func sizeClassName(_ c: UIUserInterfaceSizeClass) -> String {
        switch c {
        case .compact: return "compact"
        case .regular: return "regular"
        default: return ""
        }
    }

    // MARK: - iOS 27.1 动态派发

    /// `reservedRegions` 的两个枚举在 27.0 SDK 里查无出处, 原始值只能先按
    /// 文档语义给出假设 —— **selector 不存在时整段跳过**, 所以假设错了的
    /// 后果只是"老设备拿不到折痕", 不会崩也不会出脏数据。SDK 带上符号后
    /// 第一时间核对这里并换回编译期调用。
    private enum FoldAPI {
        static let selector = NSSelectorFromString("reservedRegionsWithKind:options:")
        static let kindDivision = 0
        static let kindOcclusion = 1
        static let optionIncludeInactive = 1 << 0
    }

    /// 动态调 `view.reservedRegions(kind:options:)` 并拼出 payload 片段
    /// (posture / regions / hinge)。宿主/系统不支持时返回 nil。
    ///
    /// 逐字段都经 responds 校验再取: 新 API 的 region 对象形状 (frame /
    /// isActive) 同样不在编译期可见, 全部走 KVC —— 少一个键就少一段输出,
    /// 宁可缺也不猜。
    private static func dynamicFoldPayload(in view: UIView, scale: CGFloat) -> [String: Any]? {
        guard view.responds(to: FoldAPI.selector) else { return nil }
        typealias ReservedRegionsFn = @convention(c) (AnyObject, Selector, Int, Int) -> NSArray
        guard let imp = view.method(for: FoldAPI.selector) else { return nil }
        let call = unsafeBitCast(imp, to: ReservedRegionsFn.self)
        let divisions = call(view, FoldAPI.selector, FoldAPI.kindDivision, FoldAPI.optionIncludeInactive)
        let occlusions = call(view, FoldAPI.selector, FoldAPI.kindOcclusion, FoldAPI.optionIncludeInactive)

        var payload: [String: Any] = [:]

        let divRegions = regionList(divisions, kind: "division", scale: scale)
        let occRegions = regionList(occlusions, kind: "occlusion", scale: scale)
        let regions = divRegions + occRegions
        if !regions.isEmpty {
            payload["regions"] = regions
        }

        // 姿态: division 里"有宽度且 active"的那条 = 设备真的折着。
        // isActive 由系统给 (半开 active / 全开 inactive); 不按宽度反推姿态,
        // 那是 gfx/mobile.PostureFrom 的活儿 (那边有测试面, 这边没有)。
        let hasActive = divisions.contains {
            ($0 as? NSObject).flatMap { boolOf($0, "isActive") } == true
        }
        if hasActive {
            payload["posture"] = "half-open"
        } else if !divRegions.isEmpty {
            // 有折痕但当前没折 ⇒ 平展。折痕已随 includeInactive 报出去,
            // 脚本侧 hasFold() 恒为 true, 列数不会折叠/平展来回跳。
            payload["posture"] = "flat"
        }
        // divisions 为空 ⇒ 不是折叠设备 (或外屏), 不写 posture —— 交给 Go 侧
        // 判成 flat + Foldable=false。

        // hinge: 只报第一条 division 的几何 (内核对 Display.Hinge 就是这么用
        // 的 —— 单折痕模型的表达力上限, 多折痕留到多段折叠区真正出现时再扩)。
        if let first = divRegions.first {
            var hinge: [String: Any] = [
                "x": first["x"] ?? 0,
                "y": first["y"] ?? 0,
                "width": first["width"] ?? 0,
                "height": first["height"] ?? 0,
            ]
            // 方向按设备像素比较: 宽 ≥ 高是横带 (landscape 折), 否则竖带。
            let fw = (first["width"] as? Int) ?? 0
            let fh = (first["height"] as? Int) ?? 0
            hinge["orientation"] = fw >= fh ? "horizontal" : "vertical"
            payload["hinge"] = hinge
        }

        return payload
    }

    /// NSArray(region 对象) → 内核 regions 数组。坐标点 → 设备像素。
    private static func regionList(_ list: NSArray, kind: String, scale: CGFloat) -> [[String: Any]] {
        var out: [[String: Any]] = []
        for (i, obj) in list.enumerated() {
            guard let r = obj as? NSObject,
                  let nsFrame = r.value(forKey: "frame") as? NSValue else { continue }
            let f = nsFrame.cgRectValue
            var item: [String: Any] = [
                "id": "\(kind)-\(i)",
                "kind": kind,
                "x": Int(f.minX * scale),
                "y": Int(f.minY * scale),
                "width": Int(f.width * scale),
                "height": Int(f.height * scale),
            ]
            if let active = boolOf(r, "isActive") {
                item["active"] = active
            }
            out.append(item)
        }
        return out
    }

    /// KVC 取 Bool (isActive 经 NSNumber 装箱); 取不到返回 nil (键省略)。
    private static func boolOf(_ obj: NSObject, _ key: String) -> Bool? {
        obj.value(forKey: key) as? Bool
    }
}
