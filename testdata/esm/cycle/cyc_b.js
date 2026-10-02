// 循环再导出的另一半: b 星号转发 a, 同时自己导出 b。
// a <-> b 的星号环必须在解析时终止 (不能死循环)。
export * from "./cyc_a.js";
export const b = "B";
