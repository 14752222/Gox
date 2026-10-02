// testdata/ts/enum.ts —— enum 会被 esbuild 展开成多行辅助代码 (改变行数),
// 是"JS 行号 ≠ .ts 行号"最典型的构造。
//
// v1 边界: 这里**不导出** enum。esbuild 把 `export enum` 转成
// `export var Color; (function(Color){…})`，而引擎 parser 目前只支持
// `export let/const/function` (不支持 export var/class) —— 导出 enum 会在解析
// 期报 "unexpected token after export: VAR"。见 docs/typescript.md，导出值请走
// 包裹函数 (如 green()) 或 `export const green = () => Color.Green`。
// enum 本身的运行与行映射不受影响 (throw_module.ts 用的是同一写法)。

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
