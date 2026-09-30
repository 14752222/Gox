// gox.d.ts —— gox API 与 JSX 元素的类型声明（给 IDE / tsc 检查用）。
//
// gox 引擎运行时不需要这份文件：.tsx 在加载时由引擎自动剥离类型（esbuild
// 转译，JSX 原样保留给引擎自己的降级管线）。这里声明的只是"类型世界"：
//   - 内置元素（<window> <row> <text> ...）走宽松索引签名，任何标签/属性都合法；
//   - 常用事件 prop（onInput / onKeyDown）给了具体形态，回调参数不用手标 any；
//   - "gox" 模块按引擎实际导出面声明 —— 与 docs/gui-guide.md 对齐。
//
// 本文件刻意**没有顶层 import/export**（保持全局脚本形态）：JSX 命名空间必须
// 全局可见，.tsx 才认 <window> 这类内置标签。引擎不读 tsconfig.json。

// ===== gox 模块 =====

declare module "gox" {
  // JSX 渲染工厂。parser 把 <tag> 降级成 h("tag", props, ...children)；
  // 文件里没绑定时编译器会自动补 import { h } from "gx/gfx"。
  export function h(tag: any, props?: any, ...children: any[]): any;

  // 建窗口并挂根组件，返回窗口句柄。只有全部窗口关闭进程才退出。
  export interface GoxWindowHandle {
    close(): void;
    isClosed(): boolean;
    title(): string;
    setTitle(t: string): void;
    resize(w: number, h: number): void;
  }
  export function render(node: any, opts?: any): GoxWindowHandle;

  // signal：返回 [getter, setter] 二元组（getter 自带 .set，可直接喂 model 指令）。
  export function createSignal<T>(value: T): [
    { (): T; set(v: T | ((prev: T) => T)): void },
    (v: T | ((prev: T) => T)) => void,
  ];

  // 路由（gx/router 经 gox 聚合导出）。页面按路径标识。
  export interface GoxRoute {
    path: string;
    params?: Record<string, string>;
  }
  export interface GoxRouter {
    currentRoute(): GoxRoute;
  }
  export function createRouter(cfg: {
    routes: Array<{
      path: string;
      component?: (...args: any[]) => any;
      redirect?: string;
      keepAlive?: boolean;
    }>;
    initial?: string;
  }): GoxRouter;

  // 页面体内读"本页所属那条路由"（signal 语义的取值函数）。
  export function useRoute(): () => GoxRoute;

  // 内置组件（props 宽松 —— 具体形态见 docs/gui-guide.md）。
  export function RouterView(props?: any): any;
  export function RouterLink(props?: any): any;
  export function Switch(props?: any): any;
  export function Match(props?: any): any;

  // 其余 gx/* 能力保持开放，按需再收紧。
  export const gx: any;
}

// ===== 事件与 JSX 元素 =====

// 常用事件回调给了具体参数形态，其余属性走索引签名（任何标签/属性都合法 ——
// 宽松是刻意的：内置元素的 prop 面有几百个，宁可少报错也不误报）。
interface GoxElementProps {
  onClick?: (e?: any) => void;
  onInput?: (e: { value: any }) => void;
  onChange?: (e: { value: any }) => void;
  onKeyDown?: (e: { key: string }) => void;
  onWheel?: (e: { deltaY: number; shift: boolean }) => void;
  [prop: string]: any;
}

declare namespace JSX {
  type Element = any;
  interface ElementChildrenAttribute {
    children: {};
  }
  interface IntrinsicElements {
    [tag: string]: GoxElementProps;
  }
}
