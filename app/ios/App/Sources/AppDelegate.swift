//
//  AppDelegate.swift
//  Gox
//
//  注: 新 SDK (iOS 27) 构建的应用**强制要求 UIScene 生命周期**, 旧式
//  "AppDelegate 自持 window" 模式会直接启动失败 (实测报
//  "UIScene life cycle is required for apps built with this SDK"),
//  所以窗口创建在 SceneDelegate 里。
//

import UIKit

@main
final class AppDelegate: UIResponder, UIApplicationDelegate {

    func application(_ application: UIApplication,
                     didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil) -> Bool {
        true
    }

    // MARK: - UISceneSession 生命周期

    func application(_ application: UIApplication,
                     configurationForConnecting connectingSceneSession: UISceneSession,
                     options: UIScene.ConnectionOptions) -> UISceneConfiguration {
        let config = UISceneConfiguration(name: nil, sessionRole: connectingSceneSession.role)
        config.delegateClass = SceneDelegate.self
        return config
    }

    /// 界面方向: 初始只竖屏; gx/app 的 setOrientation() 会改
    /// GoxNativeHost.orientationMask ("portrait"/"landscape"/"auto"), 这里跟着
    /// 返回。注意 Info.plist 的 UISupportedInterfaceOrientations 必须包含请求
    /// 的方向, 否则系统直接忽略 (横屏请求需要那两个键)。
    func application(_ application: UIApplication,
                     supportedInterfaceOrientationsFor window: UIWindow?) -> UIInterfaceOrientationMask {
        GoxNativeHost.orientationMask
    }
}

final class SceneDelegate: UIResponder, UIWindowSceneDelegate {

    var window: UIWindow?

    func scene(_ scene: UIScene, willConnectTo session: UISceneSession,
               options connectionOptions: UIScene.ConnectionOptions) {
        guard let ws = scene as? UIWindowScene else { return }
        let win = UIWindow(windowScene: ws)
        win.rootViewController = GoxViewController()
        win.makeKeyAndVisible()
        window = win
    }
}
