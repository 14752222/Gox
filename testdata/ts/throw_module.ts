// testdata/ts/throw_module.ts —— 运行时错误定位夹具 (被 throw_entry.ts import)。
// throw 在 boom() 函数体内; 前面的 enum 展开会让 JS 行号大于 .ts 行号,
// 映射必须把报错帧翻回 throw 所在的 .ts 行 (测试按源码动态算该行号)。

enum Marker {
  A,
  B,
}

export function boom(): number {
  throw new Error("fixture-boom");
}
