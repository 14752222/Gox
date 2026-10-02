// 验证: 星号再导出的对外可见集合与取值。
// 期望: 键序稳定为 "a,b"; a 是本地 "LOCAL" (不被星号覆盖); b 来自星号源 "B";
// default 不被转发 (typeof 为 "undefined")。
import * as ns from "./star_local.js";

Object.keys(ns).join(",") + "|" + ns.a + ns.b + "|" + typeof ns.default;
