// 根组件：面包屑 + （文件列表 | 预览）+ 错误条 + 状态行。
import { h } from "gox";
import { colors, space, font } from "./theme.js";
import { cwd, entries, selected, preview, error, openDir, goUp, select } from "./store.js";
import { crumbs, formatSize, formatTime } from "./lib/fsx.js";

// Crumb: 一段面包屑。点击跳回该段。
const Crumb = (p) => {
  return (
    <button padding={4} font={font.sm} onClick={() => openDir(p.path)}>{p.name}</button>
  );
}

const Breadcrumb = () => {
  return (
    <row gap={2} padding={space.sm} alignItems="center" background={colors.sidebar}>
      <button padding={4} font={font.sm} onClick={goUp}>⬆ 上级</button>
      <view each={() => crumbs(cwd())} key="path">
        {(c) => <Crumb name={c.name} path={c.path} />}
      </view>
    </row>
  );
}

// FileRow: 一行文件/目录。
const FileRow = (p) => {
  const ent = p.ent;
  return (
    <row
      gap={space.sm}
      padding={6}
      alignItems="center"
      background={() => (selected() === ent.path ? colors.selBg : colors.panel)}
      onClick={() => select(ent)}
    >
      <text width={220} font={font.md} color={() => (ent.isDir ? colors.dirText : colors.text)}>
        {() => (ent.isDir ? "📁 " : "📄 ") + ent.name}
      </text>
      <text width={90} font={font.sm} color={colors.muted}>{() => (ent.isDir ? "" : formatSize(ent.size))}</text>
      <text font={font.sm} color={colors.muted}>{() => formatTime(ent.mtimeMs)}</text>
    </row>
  );
}

const FileList = () => {
  return (
    <scroll width={560} height={430}>
      <column>
        <view each={entries} key="path" fallback={<text padding={space.md} color={colors.muted}>（空目录）</text>}>
          {(e) => <FileRow ent={e} />}
        </view>
      </column>
    </scroll>
  );
}

const Preview = () => {
  return (
    <column gap={space.xs} padding={space.sm} background={colors.panel}>
      <text font={font.sm} color={colors.muted}>{() => (selected() === null ? "（未选中文件）" : selected())}</text>
      <scroll width={260} height={380}>
        <text width={250} wrap font={font.sm} color={colors.text}>
          {() => (preview().note !== "" ? preview().note : preview().text)}
        </text>
      </scroll>
    </column>
  );
}

export function App() {
  return (
    <column background={colors.bg}>
      <Breadcrumb />
      <view show={() => error() !== ""}>
        <row padding={space.sm} background={colors.errBg}>
          <text font={font.sm} color={colors.errText}>{error}</text>
        </row>
      </view>
      <row gap={space.md} padding={space.md}>
        <FileList />
        <separator vertical height={430} />
        <Preview />
      </row>
      <row padding={space.sm} background={colors.sidebar}>
        <text font={font.xs} color={colors.muted}>
          {() => entries().length + " 项 · " + cwd()}
        </text>
      </row>
    </column>
  );
}
