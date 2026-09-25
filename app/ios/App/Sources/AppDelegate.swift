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

    /// 只支持竖屏的初始版本: 旋转走 viewWillTransition 的重绑缓冲路径,
    /// 方向锁先收窄, 等旋转实测没问题再放开。
    func application(_ application: UIApplication,
                     supportedInterfaceOrientationsFor window: UIWindow?) -> UIInterfaceOrientationMask {
        .portrait
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
