// 循环再导出的一半: a 星号转发 b, 同时自己导出 a。
export * from "./cyc_b.js";
export const a = "A";
