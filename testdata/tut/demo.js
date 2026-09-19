// Gox API 实现教程 —— 配套演示脚本
// 运行: ./Gox.exe testdata/tut/demo.js

// ===== 第2章: 实现第一个 API (Math.hypot) =====
console.log("hypot(3,4) =", Math.hypot(3, 4));          // 5
console.log("hypot() =", Math.hypot());                  // 0
console.log("hypot(1, NaN) =", Math.hypot(1, NaN));      // NaN
console.log("hypot('6', 8) =", Math.hypot("6", 8));      // 10 (字符串强制转换)

// ===== 第3章: 数据转换 (stats API) =====
const arr = [4, 1, 7, 2, 9];
console.log("stats.sum =", stats.sum(arr));              // 23
const d = stats.describe(arr);
console.log("describe: count=" + d.count + " min=" + d.min +
            " max=" + d.max + " mean=" + d.mean);

// ===== 第4章: 错误处理 =====
try {
  stats.sum("not an array");                             // TypeError → throw
} catch (e) {
  console.log("caught:", e.name, "-", e.message);
}
try {
  stats.describe([]);                                    // RangeError → throw
} catch (e) {
  console.log("caught:", e.name, "-", e.message);
}
const err = new Error("as value");                       // Error 构造器返回值
console.log("new Error is value:", err.message, "| instanceof:", err instanceof Error);

// ===== 第5章: 异步 API (delay / Promise) =====
// 注意: 当前 async 实现中，await 一个被拒绝的 Promise 会让外层 async 函数的
// Promise 进入 rejected 状态，但不会恢复生成器执行到 try/catch 内部，
// 因此异步错误的捕获用 .catch 链式写法 (详见教程 5.4 节"已知限制")。
async function main() {
  console.log("start");
  await delay(30, "30ms later");
  console.log("after 30ms");
  const v = await delay(10, 42);
  console.log("delay resolved with:", v);
}
main().then(function() {
  console.log("done");
});
