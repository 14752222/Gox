// ===== TabBar —— 移动端底部导航 (Phase 1 骨架 + Phase 2 平台差异) =====
//
// 用户态组件 (纯 JS, 零内核改动)。形态: 底部一排 tab 项, row + flexGrow 均分,
// 每项 = 图标 + 短标签; 选中态 = 强调色 + 字号近似加粗 (内核 text 无 bold)。
//
// 平台差异在这里落地的部分:
//   - 底部安全区: paddingBottom = useInsets().bottom, 桌面 insets 恒 0 无需分支。
//     注意**不要**再叠加键盘 insets —— 软键盘弹起时 AppShell 会把整个底栏
//     show={false} 隐藏, 二者不会同时生效 (内核 safeAreaStyle 的注释明确
//     "键盘 insets 与底部 insets 相加会顶出一截")。
//   - badge: absolute 叠加小红点/数字, >99 显示 99+; 由每项的 badge 函数
//     (signal 驱动) 响应式刷新。
//   - keyed 渲染: each + key="key", badge 变化只重渲染对应项的内容。
//
// 本组件保持"哑"组件: active / onChange / insets 由外部 (AppShell) 注入,
// 便于单测与在非 Shell 场景复用。
import { h } from "gx/gfx";
import { useInsets } from "gx/viewport";
import { IconView } from "./icons.js";

// badge 数字 → 显示文本 (纯函数, tabbar_logic_test.js 覆盖)。
//   0 / 负数 / NaN → "" (不渲染徽标); > 99 → "99+"; 其余原样。
export function formatBadge(n) {
  const num = Math.floor(Number(n));
  if (isNaN(num) || num <= 0) return "";
  if (num > 99) return "99+";
  return String(num);
}

export function TabBar(p) {
  const items = p.items || [];
  const active = p.active || function () { return -1; };
  const onChange = p.onChange || function () {};
  const iconSize = p.iconSize || 24;
  const barHeight = p.barHeight || 56;
  const activeColor = p.activeColor || "#1a5fb4";
  const idleColor = p.idleColor || "#5a6470";

  // gx/viewport 的 useInsets() 本身就是取值函数: 每次调用 = 订阅一次 +
  // 读一次快照 (桌面 insets 恒 0, 无需分支), 所以在高度/内边距的
  // 响应式读取点**现调现读**, 不能存下返回值再调用。
  const badgeOf = (item) =>
    formatBadge(typeof item.badge === "function" ? item.badge() : 0);

  return (
    <row
      width={"100%"}
      height={() => barHeight + useInsets().bottom}
      paddingBottom={() => useInsets().bottom}
      background={p.background || "#ffffff"}
      border={p.borderColor || "#e2e6ea"}
    >
      {/* each + key="key": 按项的 key 配对复用; item 引用不变时只有
          行内函数 prop (badge / 选中色) 就地重渲染, 不整表重建 */}
      <column
        each={items}
        key="key"
        flexGrow={1}
        height={"100%"}
      >
        {(item, i) => (
          <column
            flexGrow={1}
            width={"100%"}
            height={"100%"}
            alignItems="center"
            justifyContent="center"
            gap={2}
            onClick={() => onChange(i, item)}
          >
            {/* 图标容器: 徽标的绝对定位基准 (徽标 escapeClipping 出盒绘制) */}
            <rect width={iconSize} height={iconSize}>
              <IconView
                icon={item.icon}
                size={iconSize}
                color={() => (active() === i ? activeColor : idleColor)}
              />
              <view show={() => badgeOf(item) !== ""}>
                <rect
                  position="absolute"
                  left={iconSize - 9}
                  top={-3}
                  width={18}
                  height={12}
                  radius={6}
                  background="#e5484d"
                  escapeClipping={true}
                >
                  <text font={9} color="#ffffff" width={18}>
                    {() => badgeOf(item)}
                  </text>
                </rect>
              </view>
            </rect>
            {/* 选中态: 色变化 + 字号 +1 (近似"加粗", 内核无 bold) */}
            <text
              font={() => (active() === i ? 12 : 11)}
              color={() => (active() === i ? activeColor : idleColor)}
            >
              {() => item.title}
            </text>
          </column>
        )}
      </column>
    </row>
  );
}
