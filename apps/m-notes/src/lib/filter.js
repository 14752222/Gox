// 纯逻辑：过滤与新笔记构造。不 import "gox"。

// filterNotes 按关键词过滤（大小写不敏感，空串全过）。
export function filterNotes(notes, kw) {
  const k = String(kw || "").toLowerCase();
  if (k === "") return notes;
  const out = [];
  for (let i = 0; i < notes.length; i++) {
    if (notes[i].text.toLowerCase().indexOf(k) >= 0) out.push(notes[i]);
  }
  return out;
}

// makeNote 造一条笔记（首行截 40 字当标题）。
export function makeNote(seq, text) {
  const oneLine = String(text).split("\n")[0];
  return {
    id: "n" + seq,
    text: String(text),
    title: oneLine.length > 40 ? oneLine.slice(0, 40) + "…" : oneLine,
    createdAt: Date.now(),
  };
}

// fmtDate ms → "M月D日 HH:MM"。
export function fmtDate(ms) {
  const d = new Date(ms);
  const p = (x) => (x < 10 ? "0" + x : "" + x);
  return d.getMonth() + 1 + "月" + d.getDate() + "日 " + p(d.getHours()) + ":" + p(d.getMinutes());
}
