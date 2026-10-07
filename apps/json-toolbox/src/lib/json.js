// JSON 工具箱的纯逻辑层 —— 不 import gox，可脱离 UI 单独跑。
//
// 为什么这里有一个自研的 locateError 扫描器：
//   Gox 的 JSON.parse 走 Go 的 encoding/json，错误消息是
//   `invalid character '}' looking for beginning of object key string`，
//   **不带行列号**（错误对象的 Object.keys 还是空的，只有 message/name）。
//   而对 JSON 工具来说"第几行第几列错了"才是核心价值，所以这里自己扫一遍定位。
//
//   策略：**JSON.parse 判成败，locateError 给位置** ——
//   合法性永远以原生解析器为准（它才是规范），自研扫描器只负责把"哪里错"说得更具体；
//   万一扫描器没看出问题，就退回原生的 message（宁可没有位置，也不能给出错的位置）。
//
// ⚠️ 扫描器把游标 i / 行号 / 列号放在**一个对象**里而不是三个 `let`：
//   Gox 当前对"被闭包捕获的外层可变变量"存在读写不一致（读方拿到的是快照、写方改的是
//   副本），表现为扫描循环里 i 不推进 —— 直接死循环。证据与最小复现见仓库看板单
//   （多层嵌套闭包写外层 let 静默失效）。用对象承载状态可绕开：对象引用只读一次，
//   之后全是属性读写。**改这段代码时别把状态改回 `let`。**

const INDENT_TEXT = { two: "  ", four: "    ", tab: "\t" };

export function indentText(key) {
  const v = INDENT_TEXT[key];
  return v === undefined ? "  " : v;
}

const DIGITS = "0123456789";
const isDigit = (c) => c !== "" && DIGITS.indexOf(c) >= 0;

// ---------------------------------------------------------------------------
// 解析 / 校验
// ---------------------------------------------------------------------------

// parseText: 把文本解析成值。成功 {ok:true, value}，失败 {ok:false, message, line, col}
export function parseText(text) {
  if (text.trim() === "") {
    return { ok: false, message: "输入是空的", line: 0, col: 0 };
  }
  try {
    return { ok: true, value: JSON.parse(text) };
  } catch (e) {
    const loc = locateError(text);
    if (loc.ok === false) {
      return { ok: false, message: loc.message, line: loc.line, col: loc.col };
    }
    // 扫描器没看出问题（它的覆盖可能不如规范严）—— 退回原生消息，不给位置
    const msg = e && e.message ? e.message : "JSON 解析失败";
    return { ok: false, message: msg, line: 0, col: 0 };
  }
}

export function validateText(text) {
  const r = parseText(text);
  return r.ok ? { ok: true } : r;
}

export function formatJson(text, indentKey) {
  const r = parseText(text);
  if (!r.ok) return r;
  return { ok: true, text: JSON.stringify(r.value, null, indentText(indentKey)) };
}

export function minifyJson(text) {
  const r = parseText(text);
  if (!r.ok) return r;
  return { ok: true, text: JSON.stringify(r.value) };
}

export function statsOf(text) {
  if (text === "") return { chars: 0, lines: 0 };
  return { chars: text.length, lines: text.split("\n").length };
}

// ---------------------------------------------------------------------------
// 路径查询：a.b[0].c / $.a["b"] / [0][1]
// ---------------------------------------------------------------------------

// parsePathTokens: 路径文本 → token 数组；写法不合法返回 null
export function parsePathTokens(path) {
  let s = path.trim();
  if (s === "") return null;
  if (s[0] === "$") s = s.slice(1);

  const tokens = [];
  let i = 0;
  while (i < s.length) {
    const c = s[i];
    if (c === ".") {
      i = i + 1;
      continue;
    }
    if (c === "[") {
      const j = s.indexOf("]", i);
      if (j < 0) return null;
      const inner = s.slice(i + 1, j).trim();
      if (inner === "") return null;
      const first = inner[0];
      if (first === '"' || first === "'") {
        tokens.push(inner.slice(1, -1));
      } else {
        const num = Number(inner);
        if (Number.isNaN(num)) return null;
        tokens.push(num);
      }
      i = j + 1;
      continue;
    }
    // 读一段名字（到 . 或 [ 为止）
    let j = i;
    while (j < s.length && s[j] !== "." && s[j] !== "[") j = j + 1;
    const name = s.slice(i, j).trim();
    if (name !== "") tokens.push(name);
    i = j;
  }
  if (tokens.length === 0) return null;
  return tokens;
}

// pathPrefix: token 前 n 项的展示形式（错误消息里用）
function pathPrefix(tokens, end) {
  let s = "";
  for (let k = 0; k < end; k = k + 1) {
    const t = tokens[k];
    if (typeof t === "number") s = s + "[" + t + "]";
    else s = s === "" ? t : s + "." + t;
  }
  return s === "" ? "根" : s;
}

// queryValue: 在已解析的值上按路径取值
export function queryValue(root, path) {
  const tokens = parsePathTokens(path);
  if (tokens === null) {
    return { ok: false, message: "路径写法不对，形如 a.b[0].c 或 $.a[\"b\"]" };
  }
  let cur = root;
  for (let k = 0; k < tokens.length; k = k + 1) {
    const t = tokens[k];
    const walked = pathPrefix(tokens, k);
    if (cur === null || cur === undefined) {
      return { ok: false, message: "走到 " + walked + " 就是 null 了，不能再往下取" };
    }
    if (typeof cur !== "object") {
      return { ok: false, message: walked + " 是" + typeof cur + "，取不了属性" };
    }
    cur = cur[t];
  }
  if (cur === undefined) return { ok: false, message: "路径不存在：" + path };
  return { ok: true, value: cur };
}

export function queryByPath(text, path) {
  const r = parseText(text);
  if (!r.ok) return r;
  return queryValue(r.value, path);
}

// 取值的展示形式：字符串加引号，其余走 JSON
export function valueToDisplay(v) {
  const s = JSON.stringify(v);
  return s === undefined ? String(v) : s;
}

// ---------------------------------------------------------------------------
// 错误定位扫描器
// ---------------------------------------------------------------------------

// locateError: 扫一遍源文本，返回第一个语法问题的 {ok:false, line, col, message}
// 全部通过则 {ok:true}。行号列号从 1 开始；列按**字符**计（不按显示宽度）。
export function locateError(src) {
  // 状态打包成对象 —— 见文件头注释，这是绕开闭包写外层变量缺陷的必要写法
  const st = { i: 0, line: 1, col: 1 };

  const fail = (msg) => ({ ok: false, line: st.line, col: st.col, message: msg });
  const peek = () => (st.i < src.length ? src[st.i] : "");
  const next = () => {
    const c = src[st.i];
    st.i = st.i + 1;
    if (c === "\n") {
      st.line = st.line + 1;
      st.col = 1;
    } else {
      st.col = st.col + 1;
    }
    return c;
  };
  const skipWs = () => {
    while (st.i < src.length) {
      const c = src[st.i];
      if (c === " " || c === "\t" || c === "\n" || c === "\r") next();
      else break;
    }
  };

  const parseString = () => {
    next(); // 开引号
    while (st.i < src.length) {
      const c = next();
      if (c === "\\") {
        if (st.i < src.length) next();
        continue;
      }
      if (c === '"') return null;
    }
    return fail('字符串没有闭合（缺少结尾的 "）');
  };

  const parseNumber = () => {
    const start = st.i;
    while (st.i < src.length) {
      const c = src[st.i];
      if (isDigit(c) || c === "-" || c === "+" || c === "." || c === "e" || c === "E") next();
      else break;
    }
    if (st.i === start) return fail("这里不是一个合法的数字");
    return null;
  };

  const parseWord = (word) => {
    if (src.slice(st.i, st.i + word.length) !== word) {
      return fail("无效的字面量，这里应该是 " + word);
    }
    for (let k = 0; k < word.length; k = k + 1) next();
    return null;
  };

  const parseValue = () => {
    skipWs();
    const c = peek();
    if (c === "") return fail("内容意外结束");
    if (c === "{") return parseObject();
    if (c === "[") return parseArray();
    if (c === '"') return parseString();
    if (c === "t") return parseWord("true");
    if (c === "f") return parseWord("false");
    if (c === "n") return parseWord("null");
    if (c === "-" || isDigit(c)) return parseNumber();
    if (c === "'") return fail("JSON 的字符串必须用双引号，不能用单引号");
    if (c === ",") return fail("这里多了一个逗号");
    return fail('这里不该出现字符 "' + c + '"');
  };

  const parseObject = () => {
    next(); // {
    skipWs();
    if (peek() === "}") {
      next();
      return null;
    }
    while (true) {
      skipWs();
      const c = peek();
      if (c !== '"') {
        if (c === "}") return fail("多了一个逗号（} 前面不该有 ,）");
        if (c === "") return fail("对象没有闭合（缺少 }）");
        return fail("对象的键必须用双引号包起来");
      }
      const e1 = parseString();
      if (e1) return e1;
      skipWs();
      if (peek() !== ":") return fail("键后面缺少冒号 :");
      next();
      const e2 = parseValue();
      if (e2) return e2;
      skipWs();
      const after = peek();
      if (after === ",") {
        next();
        continue;
      }
      if (after === "}") {
        next();
        return null;
      }
      if (after === "") return fail("对象没有闭合（缺少 }）");
      return fail('这里应该是 , 或 }，实际是 "' + after + '"');
    }
  };

  const parseArray = () => {
    next(); // [
    skipWs();
    if (peek() === "]") {
      next();
      return null;
    }
    while (true) {
      const e = parseValue();
      if (e) return e;
      skipWs();
      const after = peek();
      if (after === ",") {
        next();
        continue;
      }
      if (after === "]") {
        next();
        return null;
      }
      if (after === "") return fail("数组没有闭合（缺少 ]）");
      return fail('这里应该是 , 或 ]，实际是 "' + after + '"');
    }
  };

  const err = parseValue();
  if (err) return err;
  skipWs();
  if (st.i < src.length) return fail("内容结束后还有多余的字符");
  return { ok: true, line: 0, col: 0, message: "" };
}
