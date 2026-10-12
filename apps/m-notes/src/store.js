// 应用状态：笔记列表 + 过滤 + 持久化。signal 用索引取值（引擎纪律）。
import { createSignal } from "gox";
import { getStorage, setStorage } from "gx/storage";
import { filterNotes, makeNote } from "./lib/filter.js";

const KEY = "gox-m-notes.items";

let seq = 0;
const saved = getStorage(KEY);
let initial = Array.isArray(saved) ? saved : [];
for (let i = 0; i < initial.length; i++) {
  const n = Number(String(initial[i].id).slice(1));
  if (n > seq) seq = n;
}
if (initial.length === 0) {
  initial = [makeNote(++seq, "欢迎用 Gox 记事\n长按一条可以删除"), makeNote(++seq, "底栏会避开安全区")];
}

const notesP = createSignal(initial);
const kwP = createSignal("");
const draftP = createSignal("");

const notes = notesP[0];
const kw = kwP[0];
const draft = draftP[0];

const persist = () => {
  setStorage(KEY, notes());
}

// shown 过滤后的可见列表（派生，不另存状态）。
const shown = () => {
  return filterNotes(notes(), kw());
}

function setKw(v) { kwP[1](v); }
function setDraft(v) { draftP[1](v); }

const add = () => {
  const text = draft();
  if (String(text).trim() === "") return;
  seq++;
  notesP[1](notes().concat([makeNote(seq, text)]));
  draftP[1]("");
  persist();
}

const remove = (id) => {
  notesP[1](notes().filter((n) => n.id !== id));
  persist();
}

persist();

export { notes, kw, draft, shown, setKw, setDraft, add, remove };
