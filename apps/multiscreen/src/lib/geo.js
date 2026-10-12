// 纯逻辑：窗口几何/接续体检的文本格式化。不 import "gox"。

// geoText 句柄几何 → 一行摘要。
export function geoText(w) {
  const p = w.position();
  const b = w.bounds();
  return (
    "pos=(" + p.x + "," + p.y + ")  size=" + b.width + "×" + b.height +
    "  display=" + b.displayId + "  scale=" + b.scale
  );
}

// winListText windows() → 单行摘要。
export function winListText(list) {
  if (!list || list.length === 0) return "(无窗口)";
  return list.map((w) => w.id + ":" + w.title + "@" + w.displayId).join("   |   ");
}

// continuityText router.continuity() → 每作用域一行。
export function continuityText(scopes) {
  if (!scopes || scopes.length === 0) return "(无路由作用域)";
  return scopes
    .map((s) => s.scope + " 深度" + s.depth + " 当前 " + s.path + " 状态键[" + s.stateKeys.join(",") + "]")
    .join("\n");
}
