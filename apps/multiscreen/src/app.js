// 面板组件：home / road 两个窗口共用，断点决定控件排布形态。
import { h } from "gox";
import { RouterView, RouterLink } from "gx/router";
import { useBreakpoint, matchBreakpoint } from "gx/viewport";
import { colors, space, font } from "./theme.js";
import { router, log, winList, cont, refresh } from "./store.js";
import { geoText } from "./lib/geo.js";
import { createSignal } from "gox";

// Panel: 一个窗口的完整面板。
//   props: name（"home"|"road"）、handleRef（() => 窗口句柄）、peer（() => 对端句柄）
export function Panel(p) {
  const bp = useBreakpoint();
  const geoP = createSignal("(点 读几何)");
  const geo = geoP[0];

  const readGeo = () => geoP[1](geoText(p.handleRef()));

  // 断点换形态：窄档（手机宽度）按钮竖排，宽档横排 —— 同一份代码两端跑。
  const vertical = () => {
    bp();
    return matchBreakpoint({ sm: true, md: false, lg: false, xl: false });
  };

  const GeoButtons = () => (
    <row gap={space.sm} wrap>
      <button padding={4} onClick={readGeo}>读几何</button>
      <button padding={4} onClick={() => p.handleRef().center()}>居中本窗口</button>
      <button padding={4} onClick={() => p.handleRef().moveTo(1300, 120)}>跨屏试挪</button>
    </row>
  );

  const HandoffButtons = () => (
    <row gap={space.sm} wrap>
      <RouterLink to="/draft"><text font={font.md} color={colors.accent}>进草稿页</text></RouterLink>
      <button padding={4} onClick={() => router.handoff(p.handleRef(), p.peer())}>接续到对端</button>
      <button padding={4} onClick={() => router.handoff(p.handleRef(), p.peer(), { keepSource: true })}>
        复制到对端
      </button>
    </row>
  );

  return (
    <column gap={space.sm} padding={space.md} background={colors.bg}>
      <row gap={space.sm} alignItems="center">
        <text font={font.xl} color={colors.text}>{p.title}</text>
        <spacer flexGrow={1} />
        <tag color={colors.accent}>{() => "断点 " + bp()}</tag>
      </row>
      <text font={font.xs} color={colors.muted}>{geo}</text>
      {/* 窄档把两组按钮压窄换行（wrap），宽档自然横排 —— 断点驱动形态 */}
      <column gap={space.xs} width={() => (vertical() ? 240 : 440)}>
        <GeoButtons />
        <HandoffButtons />
      </column>
      <RouterView />
      <separator />
      <row gap={space.sm} alignItems="center">
        <button padding={4} onClick={refresh}>刷新窗口/接续</button>
        <text font={font.xs} color={colors.muted}>{winList}</text>
      </row>
      <text font={font.xs} color={colors.muted} width={420} wrap>{cont}</text>
      <column gap={2} padding={space.sm} background={colors.logBg}>
        <text font={font.xs} color={colors.muted}>
          {() => (log().length > 0 ? log().join("   |   ") : "(把窗口拖到另一块屏，这里出现换屏记录)")}
        </text>
      </column>
    </column>
  );
}
