// 应用状态与动作。
//
// 跨组件共享状态的**全部机制**就是"把 signal 建在模块作用域"—— 没有额外的 store 框架。
// 组件读 signal 即建立依赖，写 signal 即触发重渲染。派生值不另存一份，写成读 signal
// 的普通函数即可（第二份状态一定会和第一份对不上）。
//
// ⚠️ signal 一律用**索引取值**（`const p = createSignal(v); const set = p[1];`），
// 不要写 `const [x, setX] = createSignal(v)`：数组解构声明出的绑定在模块作用域下
// 被同模块函数引用时会报 `ReferenceError: setX is not defined`（getter 正常、setter
// 报未定义 —— 已最小复现，见仓库看板单）。改这段代码时别改回解构写法。
import { createSignal, openFile, saveFile, clipboardWriteText } from "gox";
import {
  formatJson,
  minifyJson,
  validateText,
  queryByPath,
  valueToDisplay,
  statsOf,
} from "./lib/json.js";

// 起手给一份能直接按 Ctrl+Enter 看到效果的样例
const SAMPLE =
  '{"name":"Gox","version":"0.9.0","stack":["Go","JavaScript"],"gui":{"renderer":"software-raster","backends":["win32","x11","cocoa"]},"test262":0.7478,"stable":true,"notes":null}';

const pInput = createSignal(SAMPLE);
const input = pInput[0];
const setInput = pInput[1];

const pOutput = createSignal("");
const output = pOutput[0];
const setOutput = pOutput[1];

const pIndent = createSignal("two");
const indent = pIndent[0];
const setIndent = pIndent[1];

const pStatus = createSignal("就绪 —— 按 Ctrl+Enter 格式化");
const status = pStatus[0];
const setStatus = pStatus[1];

const pStatusKind = createSignal("info");
const statusKind = pStatusKind[0];
const setStatusKind = pStatusKind[1];

const pWhere = createSignal("");
const where = pWhere[0];
const setWhere = pWhere[1];

const pPath = createSignal("gui.backends[1]");
const path = pPath[0];
const setPath = pPath[1];

const pPathOut = createSignal("");
const pathOut = pPathOut[0];
const setPathOut = pPathOut[1];

// --- 状态写入的三个小封装：让"什么颜色的什么消息"在一处决定 ---
function sayInfo(msg) {
  setStatus(msg);
  setStatusKind("info");
  setWhere("");
}
function sayOk(msg) {
  setStatus(msg);
  setStatusKind("ok");
  setWhere("");
}
function sayBad(msg, line, col) {
  setStatus(msg);
  setStatusKind("error");
  setWhere(line > 0 ? "第 " + line + " 行，第 " + col + " 列" : "");
}

// --- 动作 ---

export function doFormat() {
  const r = formatJson(input(), indent());
  if (!r.ok) {
    setOutput("");
    sayBad(r.message, r.line, r.col);
    return;
  }
  setOutput(r.text);
  const st = statsOf(r.text);
  sayOk("格式化完成 —— " + st.lines + " 行 / " + st.chars + " 字符");
}

export function doMinify() {
  const r = minifyJson(input());
  if (!r.ok) {
    setOutput("");
    sayBad(r.message, r.line, r.col);
    return;
  }
  setOutput(r.text);
  sayOk("已压缩成一行 —— " + r.text.length + " 字符");
}

export function doValidate() {
  const r = validateText(input());
  if (!r.ok) {
    sayBad(r.message, r.line, r.col);
    return;
  }
  sayOk("校验通过 —— 是合法的 JSON");
}

export function doClear() {
  setInput("");
  setOutput("");
  setPathOut("");
  sayInfo("已清空");
}

export function doCopyOutput() {
  const text = output() === "" ? input() : output();
  if (text === "") {
    sayInfo("没有可复制的内容");
    return;
  }
  const ok = clipboardWriteText(text);
  if (ok) sayOk("已复制到剪贴板");
  else sayBad("剪贴板不可用（后端不支持或被别的进程占着）", 0, 0);
}

export function doPullOutput() {
  if (output() === "") {
    sayInfo("结果区是空的，先格式化一次");
    return;
  }
  setInput(output());
  sayOk("已把结果回填到输入区");
}

export function doQuery() {
  const r = queryByPath(input(), path());
  if (!r.ok) {
    setPathOut("");
    sayBad(r.message, 0, 0);
    return;
  }
  setPathOut(valueToDisplay(r.value));
  sayOk("查询命中");
}

export async function doOpen() {
  const picked = await openFile({
    title: "打开 JSON 文件",
    filter: [
      { name: "JSON 文件", pattern: "*.json" },
      { name: "所有文件", pattern: "*.*" },
    ],
  });
  if (picked === null || picked === undefined) {
    sayInfo("已取消");
    return;
  }
  try {
    const text = fs.readFileSync(picked);
    setInput(text);
    setOutput("");
    setPathOut("");
    const st = statsOf(text);
    sayOk("已打开 " + picked + "（" + st.lines + " 行）");
  } catch (e) {
    sayBad("读取失败：" + (e && e.message ? e.message : String(e)), 0, 0);
  }
}

export async function doSaveAs() {
  const text = output() === "" ? input() : output();
  if (text === "") {
    sayInfo("没有可保存的内容");
    return;
  }
  const picked = await saveFile({ default: "data.json" });
  if (picked === null || picked === undefined) {
    sayInfo("已取消");
    return;
  }
  try {
    fs.writeFileSync(picked, text);
    sayOk("已保存到 " + picked);
  } catch (e) {
    sayBad("保存失败：" + (e && e.message ? e.message : String(e)), 0, 0);
  }
}

// 解构出来的 signal 用 export {} 一起导出（`export const [a, b] = ...` 这种写法不走）。
export {
  input,
  setInput,
  output,
  setOutput,
  indent,
  setIndent,
  status,
  statusKind,
  where,
  path,
  setPath,
  pathOut,
};
