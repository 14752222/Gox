// 验证: export var / let / const / class / async function / async function* 都能被
// parser 接受并正确导出 —— 这是 esbuild 把 `export enum` 降级成
// `export var Color; (function(Color){…})` 后必须吃下的形态 (M2 TS 出口)。
// 文件本身作为入口执行时 (全局模式) 顶层导出走值导出路径; 被 import 时 (模块
// 模式) 走活绑定路径。两种模式都要能跑。
export var v = 9;
export let l = 1;
export const c = 2;
export class C {
  constructor() {
    this.n = 3;
  }
}
export async function af() {
  return 4;
}
export async function* ag() {
  yield 5;
}

// 汇总成一个可断言的数值: 9 + 1 + 2 + new C().n(3) = 15
v + l + c + new C().n;
