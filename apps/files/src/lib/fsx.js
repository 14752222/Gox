// 纯逻辑：列目录 / 排序 / 格式化。不 import "gox"（lib 纪律：能脱离 UI 单测）。
import fs from "fs";

// joinPath 拼路径，兼容 *nix 与 Windows 分隔符。
export function joinPath(dir, name) {
  if (dir === "" || dir === "/") return "/" + name;
  if (dir.length === 3 && dir[1] === ":" && (dir[2] === "\\" || dir[2] === "/")) {
    return dir + name;
  }
  const sep = dir.indexOf("\\") >= 0 ? "\\" : "/";
  return dir + sep + name;
}

// parentOf 取上级路径；根目录返回 null。
export function parentOf(dir) {
  const norm = dir.replace(/[\\/]+$/, "");
  const i = Math.max(norm.lastIndexOf("/"), norm.lastIndexOf("\\"));
  if (i <= 0) return dir === "/" || /^[A-Za-z]:[\\/]?$/.test(dir) ? null : "/";
  return norm.slice(0, i);
}

// crumbs 把路径拆成面包屑段 [{name, path}]。
export function crumbs(dir) {
  const parts = dir.split(/[\\/]+/).filter((s) => s !== "");
  const winRoot = /^[A-Za-z]:$/.test(parts[0] || "");
  const sep = dir.indexOf("\\") >= 0 ? "\\" : "/";
  const out = [];
  let acc = winRoot ? "" : "/";
  for (let i = 0; i < parts.length; i++) {
    if (winRoot) {
      acc = i === 0 ? parts[0] + sep : acc + parts[i] + (i < parts.length - 1 ? sep : "");
      out.push({ name: i === 0 ? parts[0] + sep : parts[i], path: acc.replace(/[\\/]+$/, "") || parts[0] + sep });
    } else {
      acc = acc === "/" ? "/" + parts[i] : acc + "/" + parts[i];
      out.push({ name: parts[i], path: acc });
    }
  }
  return out;
}

// listDir 读目录并按"目录在前、名称升序"排序。
// 返回 { entries, error }；entries 项 {name, path, isDir, size, mtimeMs}。
export function listDir(dir) {
  let names;
  try {
    names = fs.readdirSync(dir);
  } catch (e) {
    return { entries: [], error: String(e) };
  }
  if (!Array.isArray(names)) return { entries: [], error: String(names) };
  const entries = [];
  for (let i = 0; i < names.length; i++) {
    const p = joinPath(dir, names[i]);
    let isDir = false;
    let size = 0;
    let mtimeMs = 0;
    try {
      const st = fs.statSync(p);
      isDir = !!st.isDirectory;
      size = st.size || 0;
      mtimeMs = st.mtimeMs || 0;
    } catch (e) {
      // stat 失败（悬空链接等）仍列出，按文件处理
    }
    entries.push({ name: names[i], path: p, isDir, size, mtimeMs });
  }
  entries.sort((a, b) => {
    if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
    return a.name < b.name ? -1 : a.name > b.name ? 1 : 0;
  });
  return { entries, error: "" };
}

const TEXT_EXT = [".txt", ".md", ".js", ".jsx", ".ts", ".tsx", ".json", ".go", ".css", ".html", ".xml", ".yml", ".yaml", ".toml", ".log", ".csv"];

// readPreview 读文本预览；非文本扩展名或超 64KB 给提示而不是内容。
export function readPreview(path, size) {
  const low = path.toLowerCase();
  let isText = false;
  for (let i = 0; i < TEXT_EXT.length; i++) {
    if (low.endsWith(TEXT_EXT[i])) { isText = true; break; }
  }
  if (!isText) return { text: "", note: "（二进制或不支持的类型，不预览）" };
  if (size > 64 * 1024) return { text: "", note: "（超过 64KB，不预览）" };
  try {
    return { text: String(fs.readFileSync(path)), note: "" };
  } catch (e) {
    return { text: "", note: String(e) };
  }
}

// formatSize 人类可读大小。
export function formatSize(n) {
  if (n < 1024) return n + " B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KB";
  if (n < 1024 * 1024 * 1024) return (n / 1024 / 1024).toFixed(1) + " MB";
  return (n / 1024 / 1024 / 1024).toFixed(2) + " GB";
}

// formatTime mtimeMs → "YYYY-MM-DD HH:MM"。
export function formatTime(ms) {
  if (!ms) return "";
  const d = new Date(ms);
  const p = (x) => (x < 10 ? "0" + x : "" + x);
  return d.getFullYear() + "-" + p(d.getMonth() + 1) + "-" + p(d.getDate()) + " " + p(d.getHours()) + ":" + p(d.getMinutes());
}
