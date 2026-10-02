// testdata/ts/entry.ts —— 跨 .ts import 的执行夹具。
// 期望结果: "9 green"。

import { triple, PI } from "./util.ts";
import { green, colorName } from "./enum.ts";

const base: number = triple(PI);
const name: string = colorName(green());

function label(x: number, y: string): string {
  return x + " " + y;
}

label(base, name);
