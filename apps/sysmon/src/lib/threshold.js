// 阈值判定：把采样值分成 ok / warn / danger 三档。
//
// 纯逻辑。阈值语义是「越大越危险」（速率过高、占用率过高），所以用 >= 比较。
// 返回字符串档位，颜色由 UI 层映射（theme 令牌）。
export function classify(value, warn, danger) {
  if (typeof value !== "number" || value !== value) return "ok"; // NaN → 不当告警
  if (typeof danger === "number" && value >= danger) return "danger";
  if (typeof warn === "number" && value >= warn) return "warn";
  return "ok";
}

// 三档里最坏的一档（多条序列合成一个总状态时用）。
export function worstLevel(levels) {
  let worst = "ok";
  for (let i = 0; i < levels.length; i = i + 1) {
    if (levels[i] === "danger") return "danger";
    if (levels[i] === "warn") worst = "warn";
  }
  return worst;
}
