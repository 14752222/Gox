// 逗号运算符: 返回最后一个
let a = (1, 2, 3);
console.log("a:", a);               // 3
let b = ("x", "y");
console.log("b:", b);               // y

// 副作用依次执行
let count = 0;
function f() { count++; return count; }
let c = (f(), f(), f());
console.log("c:", c, "count:", count);  // 3 3

// 与赋值结合: (a = b), c 而非 a = (b, c)
let x, y;
x = 1, y = 2;
console.log("x:", x, "y:", y);      // 1 2

// 括号内逗号
let result = (1 + 2, 3 * 4);
console.log("result:", result);     // 12

// for 循环用逗号 (需要多声明支持)
let s = 0;
for (let i = 0, j = 10; i < j; i++, j--) { s += i; }
console.log("s:", s);               // 0+1+2+3+4 = 10
