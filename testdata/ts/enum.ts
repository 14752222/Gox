// testdata/ts/enum.ts —— enum 会被 esbuild 展开成多行辅助代码 (改变行数),
// 是"JS 行号 ≠ .ts 行号"最典型的构造。
//
// v1 说明 (2026-10 更新): 引擎 parser 已支持 `export var/class/async function`，
// 所以 esbuild 把 `export enum` 转成的 `export var Color; (function(Color){…})`
// 现在可以直接跑 (parser 补齐 + var 提升)。本夹具仍**不导出** enum —— 它服务的是
// "enum 展开改变行数 → 行映射"这条用例; 要验证 export enum 见
// vm/vm_export_test.go 的 TestExportVarNoInitEnumPattern。

enum Color {
  Red,
  Green = 5,
  Blue,
}

export function colorName(c: Color): string {
  return c === Color.Green ? "green" : "other";
}

export function green(): Color {
  return Color.Green;
}
