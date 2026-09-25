// ===== SideNav —— 桌面端侧边导航 (Phase 1 骨架 + Phase 3 桌面增强) =====
//
// 形态: 左侧竖排导航栏, 图标 + 长标签, 项数不限; 选中项带左侧指示条。
// 悬停提亮 / 按压压暗是内核对可点击子树的自动反馈, 无需这里写。
//
// 桌面增强在这里落地的部分:
//   - 折叠态: 宽度收到 icon-only, 宽度变化挂 transition 补间动画;
//     标签用 show (keep-alive) 摘出布局流, 展开瞬间恢复。
//   - 折叠记忆: collapsed 信号由调用方 (AppShell) 持有并持久化到 gx/storage,
//     本组件只读 —— 保持"哑"组件。
//   - 低频项: footerItems 渲染在 <spacer flexGrow={1}/> 之后, "设置"这类
//     低频入口天然沉底。
//   - 快捷键: Ctrl+1..9 切 tab 在 AppShell 根节点 onKeyDown 统一处理
//     (事件从焦点节点沿祖先链派发, 根是所有节点的祖先), 本组件不重复注册。
import { h } from "gx/gfx";
import { IconView } from "./icons.js";

export function SideNav(p) {
  const items = p.items || [];
  const footerItems = p.footerItems || [];
  const active = p.active || function () { return -1; };
  const onChange = p.onChange || function () {};
  const collapsed = p.collapsed; // 可选: 取值函数 (signal getter)
  const isCollapsed = () => !!(collapsed && collapsed());
  const width = () => (isCollapsed() ? (p.collapsedWidth || 56) : (p.width || 208));
  const accent = p.accentColor || "#1a5fb4";
  const rowH = p.itemHeight || 40;

  // 单个导航项: 指示条 + 图标 + 标签 (折叠时标签摘出布局流)。
  const itemBox = (item) => (
    <row
      height={rowH}
      alignItems="center"
      paddingLeft={12}
      gap={10}
      onClick={() => onChange(item.index, item)}
      background={() => (active() === item.index ? (p.activeBackground || "#e8f0fe") : "#00000000")}
    >
      {/* 选中指示条: 3px 竖条, 未选中时透明 (占位, 避免图标左右跳) */}
      <rect
        width={3}
        height={rowH - 14}
        radius={1}
        background={() => (active() === item.index ? accent : "#00000000")}
      />
      <IconView
        icon={item.icon}
        size={20}
        color={() => (active() === item.index ? accent : "#5a6470")}
      />
      {/* show 是 keep-alive: 折叠只是摘出布局流, 展开时状态原样回来 */}
      <view show={() => !isCollapsed()}>
        <text
          font={13}
          color={() => (active() === item.index ? accent : "#1c2430")}
        >
          {() => item.title}
        </text>
      </view>
    </row>
  );

  return (
    <column
      width={width}
      height={"100%"}
      background={p.background || "#f7f8fa"}
      border={p.borderColor || "#e2e6ea"}
      transition={{ width: 200 }}
      paddingTop={8}
    >
      <column each={items} key="key" width={"100%"}>
        {(item) => itemBox(item)}
      </column>

      {/* 低频项沉底: spacer 吃掉中部富余空间 */}
      <spacer flexGrow={1} />
      <column each={footerItems} key="key" width={"100%"}>
        {(item) => itemBox(item)}
      </column>

      {/* 折叠开关 (可关): 按钮文案随状态切换 */}
      {p.showToggle ? (
        <button
          margin={8}
          onClick={() => (p.onToggleCollapse ? p.onToggleCollapse() : null)}
        >
          {() => (isCollapsed() ? "»" : "«")}
        </button>
      ) : null}
    </column>
  );
}
