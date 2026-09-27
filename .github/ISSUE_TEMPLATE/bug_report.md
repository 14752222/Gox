name: 缺陷报告 (Bug)
description: 引擎 / GUI / 移动端壳 / CLI 某处行为与文档承诺不符
labels: ["bug"]
body:
  - type: markdown
    attributes:
      value: |
        感谢报告！发之前请先搜一下已有 issue，避免重复。

        > 「怎么用」类问题请去 Discussions —— Issue 区只收**缺陷**与**功能请求**。

        下面凡带 **\*** 的字段都是必填；证据给得越全，定位越快。

  - type: textarea
    id: what-happened
    attributes:
      label: 缺陷描述 *
      description: 期望发生什么、实际发生什么。一句「不对」不如一张截图加一行报错。
      placeholder: |
        期望：<slider> 拖动时文本实时更新
        实际：只有第一帧的值，拖动不再变化
    validations:
      required: true

  - type: dropdown
    id: area
    attributes:
      label: 影响范围 *
      options:
        - 语言/引擎（lexer/parser/compiler/vm）
        - GUI 渲染与布局（gfx）
        - 路由（gx/router）
        - 状态/响应式（gx/solid）
        - 宿主能力（gx/device、gx/app、gx/media、gx/geo、gx/permission、gx/viewport）
        - CLI 与打包（gox build / jsbuild / 脚手架）
        - Android 壳（app/android）
        - 文档
        - 其它
    validations:
      required: true

  - type: textarea
    id: repro
    attributes:
      label: 最小复现脚本 *
      description: |
        请给**能直接运行的最小脚本**。优先用 `gox <file>.js` / `goxjs <file>.js` 复现；
        若与 GUI 相关，脚本里请包含完整的 `render(...)` 调用。
        放在 ```js 代码块里。testdata/ 里有大量可参考的示例写法。
      placeholder: |
        import { createSignal } from "gx/solid";
        import { h, render } from "gx/gfx";
        // ……
    validations:
      required: true

  - type: textarea
    id: environment
    attributes:
      label: 环境信息 *
      description: 用 `gox version`（或 `goxjs version`）与 `go version` 的输出；桌面问题附 OS；移动端问题附设备 / API 级别 / 模拟器还是真机。
      placeholder: |
        Gox v0.x.x (commit ...)
        go1.26.x windows/amd64
        Windows 11 / Android 14 (API 34, arm64 真机)
    validations:
      required: true

  - type: textarea
    id: logs
    attributes:
      label: 报错输出 / 日志
      description: |
        引擎报错请贴完整文本。**Android 上 stdout 是 /dev/null**，日志要走
        `adb logcat -s Gox:I`（tag 固定为 Gox）—— 没有日志时先跑这条命令再贴。
      render: shell

  - type: checkboxes
    id: checks
    attributes:
      label: 自查项 *
      description: 帮双方省时间的三件事。
      options:
        - label: 我搜过已有 issue，没有重复。
          required: true
        - label: 我用的是最新版 Gox（老版本已修复的问题不重开）。
          required: true
        - label: 脚本里响应式属性（value / each / show / when 等）传的是**函数**而不是快照值 —— 传值只会渲染第一帧，这是文档写明的约定，不是缺陷。
          required: false
