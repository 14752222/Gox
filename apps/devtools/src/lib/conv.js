// 纯函数层：把 gx/dev 的数据面快照转换成面板组件可用的形状。
// 不 import 任何 gx 模块 —— 这层可以脱离运行时单测。

// toTreeNodes 把一扇窗口的 devTree 节点树转成 <tree> 组件的 nodes 数据。
// key 编码成 "winId:path"，onSelect 收到后一拆就能回查 devNode/devProps。
export const toTreeNodes = (win) => [convNode(win.root, win.id)];

const convNode = (node, winId) => {
  const label = nodeLabel(node);
  const out = {
    label,
    key: `${winId}:${node.path}`,
  };
  if (node.children && node.children.length > 0) {
    out.children = node.children.map((c) => convNode(c, winId));
  }
  return out;
};

// nodeLabel 拼树行文字：tag、可选 #key、布局尺寸、截断标记。
const nodeLabel = (node) => {
  const key = node.key ? `#${node.key}` : "";
  const cut = node.truncated ? " …" : "";
  if (node.tag === "#text") {
    const t = node.text ? ` "${clip(node.text, 24)}"` : "";
    return `#text${t} [${node.box.w}x${node.box.h}]${cut}`;
  }
  const role = node.role ? ` <${node.role}>` : "";
  return `${node.tag}${key}${role} [${node.box.w}x${node.box.h}]${cut}`;
};

const clip = (s, n) => (s.length > n ? s.slice(0, n - 1) + "…" : s);

// parseSelKey 把 tree 的 key 拆回 { winId, path }。
export const parseSelKey = (key) => {
  const i = key.indexOf(":");
  if (i <= 0) return null;
  return { winId: Number(key.slice(0, i)), path: key.slice(i + 1) };
};

// fmtProps 把 devProps 返回的 props 对象展平成行（嵌套对象 JSON 一行）。
export const fmtProps = (props) => {
  if (!props) return ["(null)"];
  const keys = Object.keys(props);
  if (keys.length === 0) return ["(无 props)"];
  return keys.sort().map((k) => {
    const v = props[k];
    const s = typeof v === "object" && v !== null ? JSON.stringify(v) : String(v);
    return `${k}: ${clip(s, 200)}`;
  });
};

// fmtBox 布局盒摘要行。
export const fmtBox = (node) => {
  if (!node || !node.box) return "";
  const b = node.box;
  return `x:${b.x}  y:${b.y}  w:${b.w}  h:${b.h}`;
};

// fmtSnapshot 把 devSnapshot 拼成一行性能摘要。
export const fmtSnapshot = (s) => {
  if (!s) return "";
  const pct = (r) => `${Math.round(r * 100)}%`;
  const hit = (c) => {
    const tot = c.hits + c.misses;
    return tot === 0 ? "-" : pct(c.hits / tot);
  };
  const eff = s.solid.effects < 0 ? "n/a" : s.solid.effects;
  return (
    `帧 ${s.frame.count}（整 ${s.frame.full} / 局部 ${s.frame.partial}，` +
    `整帧占比 ${pct(s.frame.fullRatio)}） · ` +
    `节点 ${s.tree.nodes} / 深度 ${s.tree.depth} / 窗口 ${s.tree.windows} · ` +
    `img 命中 ${hit(s.imageCache)} · glyph 命中 ${hit(s.glyphCache)} · ` +
    `effects ${eff} · 警告 ${s.warnings.length}`
  );
};
