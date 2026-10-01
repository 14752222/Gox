//
//  GoxDisplayFold.swift
//  Gox
//
//  iPhone Duo (iOS 27.1) 折叠屏上报: 姿态 / 折痕 / 保留区 / 尺寸类 → libgox。
//
//  为什么单独一个文件: 这段是唯一碰 iOS 27.1 新 API (`reservedRegions`) 的地方,
//  全部包在 `#available` 里; 混进 GoxViewController 会让"哪几行是新系统才有的"
//  变得看不出来。契约的另一端是 gfx/mobile.ReportDisplayFold (解析 + 校验 +
//  归一化) 与 gfx/ios/libgox/main.go 的 `//export gox_set_display_fold`。
//
//  ── 三条纪律 (与内核注释同源, 改动前先读) ─────────────────────────────
//
//  ① **坐标一律转设备像素**: 与 reportSafeAreaInsets() 同一取舍 —— 内核侧的
//     视口/布局全在设备像素上, 点值混进去会在 Retina 上错一个 scale 倍。
//  ② **不猜**: 宿主只报"看到的", 姿态判定 (half-open / flat / folded) 与
//     折痕归一化全部交给 Go 侧 —— 那边有 `go test ./gfx/mobile` 的测试面,
//     而这里的代码在 Windows 开发机上**完全编译不到**, 写错了定位成本极高。
//  ③ **不自己算 hasFold**: 结构性折痕的判据在 gfx/mobile.StructuralRegions。
//     这里只负责把 `kind` 如实填上 (division / occlusion)。
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

        // 姿态与保留区: `view.reservedRegions(kind:options:)` 是 **iOS 27.1**
        // 引入的 (Apple 文档标 iOS 27.1+ / iPadOS 27.1+ Beta)。
        //
        // 门槛必须写 27.1 而不是 27.0: 写宽了编译器不报错, 而 27.0 的真机上
        // 这句是 **unrecognized selector → 直接崩**, 恰好在"首批用户拿到
        // iPhone Duo (出厂 27.x)"这个最不该崩的时刻。写严了顶多是老机上
        // 走不到这条分支 (那正是我们想要的降级)。
        if #available(iOS 27.1, *) {
            // .includeInactive 必须显式传: Apple 自己的两份文档**说法不一致**
            // —— Tech Talk 说默认只返回 active, 符号文档说无论 active 与否都返回。
            // 显式传过这个歧义就不存在了, 而且我们这个场景**恰恰需要** inactive:
            // 平展时那条"宽度 0 的 division" 是"这台设备折过"的唯一证据, 正是
            // 脚本侧 hasFold() 恒为 true (界面列数不来回跳) 的依据。
            let divisions = view.reservedRegions(kind: .division, options: .includeInactive)
            let occlusions = view.reservedRegions(kind: .occlusion, options: .includeInactive)

            // 姿态: division 里"有宽度且 active"的那条 = 设备真的折着。
            // `isActive` 由系统给 —— 折痕在**半开时 active、全开时 inactive**,
            // 而"平展时 inactive 且宽度为 0"。这里**不要**自己按宽度反推,
            // 那是 gfx/mobile 的活儿 (那边有测试面, 这边没有)。
            let activeDivision = divisions.first { $0.isActive }
            if activeDivision != nil {
                payload["posture"] = "half-open"
            } else if !divisions.isEmpty {
                // 有折痕但当前没折 ⇒ 平展。折痕仍要报出去 (上面 includeInactive),
                // 脚本侧的 hasFold() 因此恒为 true, 列数不会折叠/平展来回跳。
                payload["posture"] = "flat"
            }
            // divisions 为空 ⇒ 不是折叠设备 (或外屏: 外屏**连 inactive 的保留区
            // 都没有**), 不写 posture —— 交给 Go 侧判成 flat + Foldable=false。

            var regions: [[String: Any]] = []
            for (i, r) in divisions.enumerated() {
                regions.append([
                    "id": "fold-\(i)",
                    "kind": "division",
                    "x": Int(r.frame.minX * scale),
                    "y": Int(r.frame.minY * scale),
                    "width": Int(r.frame.width * scale),
                    "height": Int(r.frame.height * scale),
                    "active": r.isActive,
                ])
            }
            for (i, r) in occlusions.enumerated() {
                regions.append([
                    "id": "occlusion-\(i)",
                    "kind": "occlusion",
                    "x": Int(r.frame.minX * scale),
                    "y": Int(r.frame.minY * scale),
                    "width": Int(r.frame.width * scale),
                    "height": Int(r.frame.height * scale),
                    "active": r.isActive,
                ])
            }

            if !regions.isEmpty {
                payload["regions"] = regions
            }
            // hinge: 只报第一条 division 的几何 (内核对 Display.Hinge 就是这么用的
            // —— 单折痕模型的表达力上限, 多折痕留到多段折叠区真正出现时再扩)。
            if let first = divisions.first {
                payload["hinge"] = [
                    "x": Int(first.frame.minX * scale),
                    "y": Int(first.frame.minY * scale),
                    "width": Int(first.frame.width * scale),
                    "height": Int(first.frame.height * scale),
                    "orientation": first.frame.width >= first.frame.height
                        ? "horizontal" : "vertical",
                ]
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
}
