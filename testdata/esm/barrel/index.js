// 真实 barrel: 星号转发 a.js, 具名再导出 b.js —— npm 包 index 文件的典型形态。
// 这是 M5 (npm 纯 JS 包可用) 出口验收的关键证据。
export * from "./a.js";
export { b } from "./b.js";
