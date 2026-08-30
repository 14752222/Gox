// 无花括号单语句体
let n = 0;
let out = "";
do out += n++; while (n < 3);
console.log("single-stmt:", out);

// 函数内 do-while
function collect() {
  let x = 0;
  let acc = "";
  do { acc += x; x++; } while (x < 4);
  return acc;
}
console.log("in-func:", collect());
