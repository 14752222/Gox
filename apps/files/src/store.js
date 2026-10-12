// 应用状态与动作。signal 用索引取值（引擎纪律，见 notes/src/store.js 头注释）。
import { createSignal } from "gox";
import { listDir, parentOf, readPreview } from "./lib/fsx.js";

// 起始目录：进程工作目录。
const startDir = ".";

const cwdP = createSignal(".");
const entriesP = createSignal([]);
const selP = createSignal(null);
const previewP = createSignal({ text: "", note: "" });
const errP = createSignal("");

const cwd = cwdP[0];
const setCwd = cwdP[1];
const entries = entriesP[0];
const setEntries = entriesP[1];
const selected = selP[0];
const setSelected = selP[1];
const preview = previewP[0];
const setPreview = previewP[1];
const error = errP[0];
const setError = errP[1];

// refresh 重新读当前目录。
const refresh = () => {
  const r = listDir(cwd());
  setEntries(r.entries);
  setError(r.error);
}

// openDir 进入目录（"." 先按原样存，statSync 能解析）。
const openDir = (path) => {
  setCwd(path);
  setSelected(null);
  setPreview({ text: "", note: "" });
  const r = listDir(path);
  setEntries(r.entries);
  setError(r.error);
}

// goUp 回上级；已在根则无操作。
const goUp = () => {
  const p = parentOf(cwd());
  if (p !== null) openDir(p);
}

// select 选中一行：目录 → 进入；文件 → 读预览。
const select = (ent) => {
  if (ent.isDir) {
    openDir(ent.path);
    return;
  }
  setSelected(ent.path);
  setPreview(readPreview(ent.path, ent.size));
}

refresh();

export { cwd, entries, selected, preview, error, refresh, openDir, goUp, select };
