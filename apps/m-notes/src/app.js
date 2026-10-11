// 根组件：搜索栏 + 列表（长按删除）+ 底部输入栏（insets 感知）。
import { h } from "gox";
import { useInsets } from "gx/viewport";
import { colors, space, font } from "./theme.js";
import { kw, draft, shown, setKw, setDraft, add, remove } from "./store.js";
import { fmtDate } from "./lib/filter.js";

const insets = useInsets();

// NoteRow: 一条笔记，长按删除。
const NoteRow = (p) => {
  const n = p.note;
  return (
    <column
      gap={2}
      padding={space.md}
      background={colors.panel}
      onLongPress={() => remove(n.id)}
    >
      <text font={font.md} color={colors.text}>{n.title}</text>
      <text font={font.xs} color={colors.muted}>{() => fmtDate(n.createdAt)}</text>
      <separator />
    </column>
  );
}

export function App() {
  return (
    <column background={colors.bg}>
      {/* 顶部安全区垫片 */}
      <rect width={390} height={() => insets().top} background={colors.bar} />
      <column gap={space.sm} padding={space.sm} background={colors.bar}>
        <text font={font.xl} color={colors.text}>记事</text>
        <search
          width={358}
          value={kw}
          placeholder="搜索"
          onInput={(e) => setKw(e.value)}
        />
      </column>
      <scroll width={390} height={470}>
        <column>
          <view
            each={shown}
            key="id"
            fallback={<empty desc="没有匹配的笔记" />}
          >
            {(n) => <NoteRow note={n} />}
          </view>
        </column>
      </scroll>
      {/* 底部输入栏：paddingBottom 跟安全区走 */}
      <row
        gap={space.sm}
        padding={space.sm}
        paddingBottom={() => space.sm + insets().bottom}
        background={colors.bar}
        alignItems="center"
      >
        <input
          width={280}
          value={draft}
          placeholder="记点什么…"
          onInput={(e) => setDraft(e.value)}
        />
        <button padding={8} background={colors.accent} color="#ffffff" onClick={add}>添加</button>
      </row>
      <row padding={space.xs} background={colors.bar}>
        <text font={font.xs} color={colors.muted}>长按列表项删除</text>
      </row>
    </column>
  );
}
