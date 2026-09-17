// 可选 catch 绑定: catch 无参数
let result = "";
try {
  throw new Error("boom");
} catch {
  result = "caught-no-param";
}
console.log("1:", result);

// catch {} + finally
let r2 = "";
try {
  throw 42;
} catch {
  r2 = "inner";
} finally {
  r2 += "-finally";
}
console.log("2:", r2);

// catch 带参数仍工作
let r3 = "";
try {
  throw new Error("err");
} catch (e) {
  r3 = "with-param:" + e.message;
}
console.log("3:", r3);
