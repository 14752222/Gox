// testdata/ts/component.tsx —— TSX 组件夹具 (JSX 原样保留, 交引擎降级)。
// 用大写标签 = 组件调用, 不触发 h 缺省工厂补齐 (vm 单测环境没有注册 gx/gfx)。

import { triple } from "./util.ts";

const Inner = () => ({ kind: "box", value: triple(2) });

export const Component = () => <Inner />;

Component().kind;
