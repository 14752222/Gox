// do-while 循环测试
let i = 0;
let sum = 0;
do { sum += i; i++; } while (i < 5);
console.log("sum(0..4):", sum);

// 条件首轮即为假也应至少执行一次
let j = 10;
let count = 0;
do { count++; } while (j < 5);
console.log("runs-at-least-once:", count);

// continue 应该跳到条件检查
let k = 0;
let evens = "";
do {
  k++;
  if (k % 2 === 1) continue;
  evens += k;
} while (k < 6);
console.log("continue-evens:", evens);

// break 退出
let m = 0;
let r = "";
do {
  m++;
  if (m > 3) break;
  r += m;
} while (true);
console.log("break:", r);
