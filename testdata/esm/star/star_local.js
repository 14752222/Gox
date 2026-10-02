// 验证: `export * from "./star_src.js"` 的两个规范要点。
//   1. default 不参与星号转发 (star_src 的 default 不应出现在这里);
//   2. 星号导出不覆盖本模块已有的同名导出 —— 本文件的 a = "LOCAL" 必须赢。
export const a = "LOCAL";
export * from "./star_src.js";
