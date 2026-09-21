// 本地模块（JS 侧被 `import ... from "./tutorial_util.js"` 导入）。
//
// 相对路径的规则: 必须写 `./` 前缀与 `.js` 后缀, 基准目录是**入口脚本所在的目录**
// （等价 Go 侧 `vm.EvalFile` 的行为: 载入入口时会把模块基准路径设成它所在的目录）。
export const VERSION = "1.0.0";

export function shout(text) {
  return text.toUpperCase();
}

// 默认导出: 用 `import describe from "./tutorial_util.js"` 接
export default function describe(name) {
  return name + "@" + VERSION;
}
