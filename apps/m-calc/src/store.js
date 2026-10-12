// 应用状态：按键分发。signal 用索引取值（引擎纪律）。
import { createSignal } from "gox";
import { initial, press, backspace } from "./lib/calc.js";

const stP = createSignal(initial());
const state = stP[0];

// display 屏显字符串（派生）。
const display = () => {
  return state().display;
}

// tap 按钮点击入口。
const tap = (key) => {
  if (key === "⌫") {
    stP[1](backspace(state()));
    return;
  }
  stP[1](press(state(), key));
}

const KEYMAP = { Enter: "=", Escape: "C", Backspace: "⌫", "*": "×", "/": "÷" };

// key 键盘等价入口。
const key = (k) => {
  const mapped = KEYMAP[k] !== undefined ? KEYMAP[k] : k;
  if ((mapped >= "0" && mapped <= "9") || ".+-×÷=%C±⌫".indexOf(mapped) >= 0) {
    tap(mapped);
  }
}

export { display, tap, key };
