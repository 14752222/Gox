// 根组件：外观 / 行为 / 关于 三组设置卡。
//
// 组件不收 children（Gox 组件标签是 Comp(props, ...children) 直接调用，
// 额外参数拿不到）—— 复合内容走 content 函数 prop（与 ui/shell.js 同一手法）。
import { h } from "gox";
import { colors, space, font } from "./theme.js";
import {
  dark, accent, fontSize, autoSave, notifications, sound, quality, reminderDate,
  setDark, setAccent, setFontSize, setAutoSave, setNotifications, setSound,
  setQuality, setReminderDate,
} from "./store.js";
import { QUALITY_OPTIONS } from "./lib/prefs.js";

// Card: 一组设置的卡片容器。content 传函数。
const Card = (p) => (
  <column gap={space.sm} padding={space.md} background={colors.panel} width={560}>
    <text font={font.lg} color={colors.text}>{p.title}</text>
    <separator />
    {p.content}
  </column>
);

// Row: 标签 + 控件一行。content 传函数。
const Row = (p) => (
  <row gap={space.md} alignItems="center">
    <label width={140}>{p.label}</label>
    {p.content}
  </row>
);

export function App() {
  return (
    <scroll height={600}>
      <column background={colors.bg} gap={space.md} padding={space.md}>
        <text font={font.lg} color={colors.text}>设置</text>

        <Card title="外观" content={() => [
          <Row label="深色模式" content={() => (
            <switch checked={dark} onClick={() => setDark(!dark())} />
          )} />,
          <Row label="强调色" content={() => (
            <colorpicker value={accent} onChange={(e) => setAccent(e.value)} />
          )} />,
          <Row label="字号" content={() => (
            <row gap={space.sm} alignItems="center">
              <slider width={200} value={fontSize} min={10} max={20} onInput={(e) => setFontSize(e.value)} />
              <text font={font.sm} color={colors.muted}>{() => fontSize() + " px"}</text>
            </row>
          )} />,
          <Row label="预览" content={() => (
            <text font={() => fontSize()} color={() => accent()}>即改即存的设置面板</text>
          )} />,
        ]} />

        <Card title="行为" content={() => [
          <Row label="自动保存" content={() => (
            <switch checked={autoSave} onClick={() => setAutoSave(!autoSave())} />
          )} />,
          <Row label="通知" content={() => (
            <switch checked={notifications} onClick={() => setNotifications(!notifications())} />
          )} />,
          <Row label="提示音" content={() => (
            <switch checked={sound} onClick={() => setSound(!sound())} />
          )} />,
          <Row label="渲染质量" content={() => (
            <select value={quality} options={QUALITY_OPTIONS} onChange={(e) => setQuality(e.value)} />
          )} />,
          <Row label="提醒日期" content={() => (
            <datepicker value={reminderDate} onChange={(e) => setReminderDate(e.value)} placeholder="选择日期" />
          )} />,
        ]} />

        <Card title="关于" content={() => (
          <text font={font.sm} color={colors.muted}>
            Gox 设置面板示例 · 所有改动立即写入 gx/storage，重启后原样恢复
          </text>
        )} />
      </column>
    </scroll>
  );
}
