// gx/router 懒加载页面模块演示: 组件 + 组件级守卫都从模块导出。
//
// 这个文件存在的意义是**验证懒加载的完整契约**:
//   - default 导出就是页面组件 (路由表写 lazy(() => import("./router_page_detail.js")))
//   - beforeRouteEnter / beforeRouteLeave 作为**命名导出**提供组件级守卫
//     —— 这就是 vue-router 里 <script> 导出同名函数的等价物
//   - 页面里可以同时用 useRoute() 与 useRouteState()
//
// useRouteState 的 visits 计数是"状态袋"的示例: 它记在**历史栈项**上, 所以
// 页面被销毁重建 (例如折叠态切换) 后仍然读得回同一个值。
import { h } from "gx/gfx";
import { useRoute, useRouteState } from "gx/router";

function pushLog(bag, item) {
  const list = globalThis[bag] || (globalThis[bag] = []);
  list.push(item);
}

export function beforeRouteEnter(to, from) {
  pushLog("enterLog", "enter:" + to.path);
}

export function beforeRouteLeave(to, from) {
  pushLog("leaveLog", "leave:" + to.path);
  return true;
}

export default function DetailPage(props) {
  const route = useRoute();
  const st = useRouteState();
  const visits = (st.get("visits", 0) || 0) + 1;
  st.set("visits", visits);
  return (
    <column gap={6}>
      <text font={15}>{"detail id=" + props.param.id}</text>
      <text font={12} color="#667">{() => "path: " + route().path}</text>
      <text font={12} color="#667">{"visits=" + visits}</text>
    </column>
  );
}
