// 入口：建窗口 + 挂根组件（TypeScript 版）。
//
// 运行：npm run dev      （等价于 goxjs src/main.tsx）
//
// .tsx 直接跑：gox 在加载时自动剥离类型注解（esbuild 转译，JSX 原样保留给
// 引擎自己的降级管线），没有 node 端构建步骤，也没有 "编译产物" 这个概念 ——
// 源码即运行码。
//
// 窗口配置写在根元素上（title / width / height）。render() 返回窗口句柄
// { close(), isClosed(), title(), setTitle(t), resize(w, h) }，可以调用多次开多窗口；
// 只有**全部窗口关闭**事件循环才退出、进程才结束。
import { h, render } from "gox";
import { App } from "./app.tsx";

render(
  <window title="__PROJECT_TITLE__" width={620} height={540}>
    <App />
  </window>
);
