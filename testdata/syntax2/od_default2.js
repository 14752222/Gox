// 数组解构默认值
const [a = 1, b = 2] = [undefined, 5];
console.log("a:", a, "b:", b);      // 1 5
const [x = 10] = [];
console.log("x:", x);               // 10
const [p, q = 99] = [7];
console.log("p:", p, "q:", q);      // 7 99

// 对象解构嵌套默认
const { m, n = 8 } = { m: 3 };
console.log("m:", m, "n:", n);      // 3 8

// 别名 + 默认
const { s: sVal = 100 } = {};
console.log("sVal:", sVal);         // 100

// 默认值为表达式
let count = 5;
const { w = count * 2 } = {};
console.log("w:", w);               // 10
