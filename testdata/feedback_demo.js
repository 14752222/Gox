// S4/T09 演示: 反馈与数据类组件 (一屏看完 15 个)。
// 运行: go run . testdata/feedback_demo.js
// 现象:
//   - alert (info/success/warn/error, 可关闭) / tag / avatar / empty
//   - badge (带子节点): 角标贴在宿主右上角; value=0 自动隐藏; dot 模式
//   - spinner / skeleton: 一直转 / 一直呼吸 (靠动画心跳, 无新增定时器)
//   - pagination: 点页码派发 onChange, 完全受控 (显示值取决于 current)
//   - icon: 内置图标集 (名字见 docs/gui-guide.md 的元素表)
//   - drawer: 点按钮打开, 从右侧滑入; 点遮罩 / Esc 关闭
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";

const [page, setPage] = createSignal(3);
const [drawerOpen, setDrawerOpen] = createSignal(false);
const [showAlert, setShowAlert] = createSignal(true);
const [tags, setTags] = createSignal(["urgent", "backend", "bug"]);

render(
  <window title="Feedback & Data demo" width={560} height={620}>
    <scroll>
      <column gap={14} padding={18}>
        <text font={18}>Feedback &amp; Data components</text>

        {/* alert: 四种 level, 可关闭 */}
        {() => showAlert() ? (
          <alert level="info" closable onClose={() => setShowAlert(false)}>
            A dismissible info banner.
          </alert>
        ) : (
          <text>alert dismissed — reload to bring it back</text>
        )}
        <alert level="success">Saved successfully.</alert>
        <alert level="warn">Storage is almost full.</alert>
        <alert level="error">Upload failed.</alert>

        {/* tag: 可关闭标签 */}
        <row gap={6}>
          {() => tags().map((t) => (
            <tag
              closable
              color="#1a5fb4"
              onClose={() => setTags(tags().filter((x) => x !== t))}
            >{t}</tag>
          ))}
        </row>

        {/* badge: 角标 */}
        <row gap={24} align="center">
          <badge value={5}>
            <button><text>Inbox</text></button>
          </badge>
          <badge value={120} max={99}>
            <button><text>Caps</text></button>
          </badge>
          <badge dot>
            <button><text>Status</text></button>
          </badge>
        </row>

        {/* avatar */}
        <row gap={10} align="center">
          <avatar name="Ada Lovelace" size={40} />
          <avatar name="Bob" size={40} round color="#2ec27e" />
          <text>Ada Lovelace / Bob</text>
        </row>

        {/* icon: 内置图标集 */}
        <row gap={14} align="center">
          <icon name="home" size={24} />
          <icon name="search" size={24} />
          <icon name="user" size={24} />
          <icon name="gear" size={24} />
          <icon name="bell" size={24} />
          <icon name="heart" size={24} color="#e01b24" />
          <icon name="check" size={24} color="#2ec27e" />
          <icon name="close" size={24} color="#777" />
        </row>

        {/* spinner + skeleton */}
        <row gap={20} align="center">
          <spinner size={28} />
          <spinner size={28} color="#e01b24" />
          <text>loading…</text>
        </row>
        <skeleton rows={3} avatar />

        {/* pagination: 完全受控 */}
        <text>{() => `page = ${page()}`}</text>
        <pagination
          total={200}
          pageSize={20}
          current={() => page()}
          onChange={(e) => setPage(e.page)}
        />

        {/* empty */}
        <empty desc="Nothing here yet">
          <icon name="folder" size={44} color="#bbb" />
        </empty>

        {/* drawer: 从右侧滑入 (弹层, 挂树里即可, 靠 escapeClipping 提升到根层级) */}
        <button onClick={() => setDrawerOpen(true)}>
          <text>Open drawer</text>
        </button>
        <drawer
          open={() => drawerOpen()}
          side="right"
          width={280}
          onClose={() => setDrawerOpen(false)}
        >
          <column gap={10} padding={16}>
            <text font={16}>Drawer</text>
            <text>点遮罩或按 Esc 关闭。</text>
            <button onClick={() => setDrawerOpen(false)}>
              <text>Close</text>
            </button>
          </column>
        </drawer>
      </column>
    </scroll>
  </window>
);
