name: 功能请求 (Feature)
description: 新组件、新标准库 API、新平台、新工具
labels: ["feature"]
body:
  - type: markdown
    attributes:
      value: |
        提之前建议先翻一眼 [docs](https://github.com/14752222/Gox/tree/main/docs) ——
        有些「缺失」其实是**约定**（例如响应式 prop 必须传函数）或已有替代写法。

  - type: textarea
    id: problem
    attributes:
      label: 要解决什么问题 *
      description: 你遇到了什么障碍、现在不得不用什么别扭的写法绕。**先讲问题，再讲方案。**
      placeholder: 想给列表做隔行变色，现在只能在 each 循环里手写三元表达式。
    validations:
      required: true

  - type: textarea
    id: solution
    attributes:
      label: 期望的能力 *
      description: 想要的 API 形态 / 组件用法，给一段期望的 JSX 调用最好。
      placeholder: |
        <view each={users} striped>...</view>
    validations:
      required: true

  - type: dropdown
    id: area
    attributes:
      label: 影响范围 *
      options:
        - 语言/引擎（新语法 / 性能）
        - GUI 组件（gfx 内置元素）
        - 路由（gx/router）
        - 状态/响应式（gx/solid）
        - 宿主能力（gx/device、gx/app、gx/media、gx/geo、gx/permission、gx/viewport）
        - CLI 与打包（gox build / jsbuild / 脚手架）
        - 移动端（Android 壳 / iOS / 鸿蒙）
        - 文档与官网
        - 其它
    validations:
      required: true

  - type: checkboxes
    id: checks
    attributes:
      label: 自查项 *
      options:
        - label: 我搜过已有 issue 与 docs/，确认没有等价能力。
          required: true
        - label: 如果被实现，我愿意验证或参与实现。
          required: false
