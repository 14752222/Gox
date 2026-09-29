// 纯计算口径: fib(28) 递归 + 10 万次函数调用循环。外层计 wall time。
function fib(n) { if (n < 2) return n; return fib(n - 1) + fib(n - 2); }
let t = 0;
function add(a, b) { return a + b; }
for (let i = 0; i < 100000; i++) { t = add(t, i); }
console.log("fib=" + fib(28) + " loop=" + t);
