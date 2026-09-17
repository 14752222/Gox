const { z = 30 } = {};
console.log("z:", z);              // 30
const { a = 1, b = 2 } = { a: 5 };
console.log("a:", a, "b:", b);     // 5 2
let { x = 10 } = { x: undefined };
console.log("x:", x);              // 10 (undefined 触发默认值)
let { y = 99 } = { y: 0 };
console.log("y:", y);              // 0 (0 非 nullish, 不触发)
