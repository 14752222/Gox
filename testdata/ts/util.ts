// testdata/ts/util.ts —— 跨 .ts import 的夹具 (供 entry.ts / component.tsx 使用)。
// 类型注解会被 esbuild 剥离; 导出名要真的被 import (否则编译器剪枝, 模块不加载)。

export function triple(n: number): number {
  return n * 3;
}

export const PI: number = 3;
