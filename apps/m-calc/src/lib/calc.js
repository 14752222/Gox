// 纯逻辑：计算器按键状态机。不 import "gox"，可脱离 UI 单测。
//
// 状态：{ display, acc, op, fresh }
//   display  屏上字符串
//   acc      已累积的左操作数（null = 没有）
//   op       待执行运算符（null = 没有）
//   fresh    true = 下一次数字输入清屏（刚按完运算符或 =）

export function initial() {
  return { display: "0", acc: null, op: null, fresh: true };
}

const apply = (a, op, b) => {
  switch (op) {
    case "+": return a + b;
    case "-": return a - b;
    case "×": return a * b;
    case "÷": return b === 0 ? NaN : a / b;
  }
  return b;
}

// fmt 数字 → 屏显字符串（去掉浮点尾巴，NaN 显示"错误"）。
export function fmt(n) {
  if (typeof n !== "number" || isNaN(n)) return "错误";
  if (!isFinite(n)) return "错误";
  let s = String(Math.round(n * 1e10) / 1e10);
  if (s.length > 12) s = n.toExponential(6);
  return s;
}

// press(st, key) → 新状态。key ∈ 0-9 . C ± % + - × ÷ =
export function press(st, key) {
  const s = { display: st.display, acc: st.acc, op: st.op, fresh: st.fresh };

  if (key >= "0" && key <= "9") {
    if (s.fresh || s.display === "0" || s.display === "错误") {
      s.display = key;
    } else if (s.display.replace(/[-.]/g, "").length < 12) {
      s.display = s.display + key;
    }
    s.fresh = false;
    return s;
  }
  if (key === ".") {
    if (s.fresh) {
      s.display = "0.";
      s.fresh = false;
    } else if (s.display.indexOf(".") < 0) {
      s.display = s.display + ".";
    }
    return s;
  }
  if (key === "C") return initial();
  if (key === "±") {
    if (s.display !== "0" && s.display !== "错误") {
      s.display = s.display[0] === "-" ? s.display.slice(1) : "-" + s.display;
    }
    return s;
  }
  if (key === "%") {
    const v = Number(s.display);
    s.display = fmt(v / 100);
    s.fresh = true;
    return s;
  }
  if (key === "+" || key === "-" || key === "×" || key === "÷") {
    const v = Number(s.display);
    if (s.acc !== null && s.op !== null && !s.fresh) {
      const r = apply(s.acc, s.op, v);
      s.display = fmt(r);
      s.acc = isNaN(r) ? null : r;
    } else {
      s.acc = v;
    }
    s.op = key;
    s.fresh = true;
    return s;
  }
  if (key === "=") {
    if (s.op !== null && s.acc !== null) {
      const r = apply(s.acc, s.op, Number(s.display));
      s.display = fmt(r);
      s.acc = null;
      s.op = null;
      s.fresh = true;
    }
    return s;
  }
  return s;
}

// KEYS 按钮盘（行主序，4 列）。kind: digit | fn | op。
export const KEYS = [
  { label: "C", kind: "fn" }, { label: "±", kind: "fn" }, { label: "%", kind: "fn" }, { label: "÷", kind: "op" },
  { label: "7", kind: "digit" }, { label: "8", kind: "digit" }, { label: "9", kind: "digit" }, { label: "×", kind: "op" },
  { label: "4", kind: "digit" }, { label: "5", kind: "digit" }, { label: "6", kind: "digit" }, { label: "-", kind: "op" },
  { label: "1", kind: "digit" }, { label: "2", kind: "digit" }, { label: "3", kind: "digit" }, { label: "+", kind: "op" },
  { label: "0", kind: "digit" }, { label: ".", kind: "digit" }, { label: "⌫", kind: "fn" }, { label: "=", kind: "op" },
];

// backspace 退一格（⌫ 专用，单独出来避免混进 press 的受试一下）。
export function backspace(st) {
  const s = { display: st.display, acc: st.acc, op: st.op, fresh: st.fresh };
  if (s.fresh || s.display === "错误") return s;
  s.display = s.display.length > 1 ? s.display.slice(0, s.display.length - 1) : "0";
  if (s.display === "-" || s.display === "") s.display = "0";
  return s;
}
