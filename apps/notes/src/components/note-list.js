// 左侧笔记列表 —— `each` 指令 + keyed 复用的最小示例。
//
// `each={notes}` **必须收取值函数**（signal 本身就是函数，写 `each={notes()}` 只拿到
// 一张快照，之后再加笔记界面不会动），`key="id"` 是复用判据（apps/README 第 3 条：
// 不写 key 会命中 lint 规则 `each-no-key`）。
import { h } from "gox";
import { colors, space, font } from "../theme.js";
import { notes, selId, select } from "../store.js";

export function NoteList() {
  return (
    <column width={200} gap={space.sm} padding={space.sm} background={colors.sidebar}>
      <row gap={space.xs} alignItems="center">
        <text font={font.sm} color={colors.muted}>笔记</text>
        <spacer flexGrow={1} />
        <text font={font.xs} color={colors.muted}>{() => notes().length + " 篇"}</text>
      </row>
      <view each={notes} key="id">
        {(n) => (
          <list-item
            selected={() => n.id === selId()}
            onClick={() => select(n.id)}
          >
            <text
              font={font.md}
              color={() => (n.id === selId() ? colors.accentFg : colors.text)}
              width={170}
            >{n.title}</text>
          </list-item>
        )}
      </view>
    </column>
  );
}
