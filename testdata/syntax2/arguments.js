// arguments 对象测试
function sum() {
  let total = 0;
  for (let i = 0; i < arguments.length; i++) {
    total += arguments[i];
  }
  return total;
}
console.log("sum(1,2,3):", sum(1, 2, 3));        // 6
console.log("sum():", sum());                     // 0
console.log("sum(10):", sum(10));                 // 10

// arguments 与参数个数
function count() {
  return arguments.length;
}
console.log("count:", count("a", "b", "c"));     // 3

// arguments 类型
function typeCheck() {
  return Array.isArray(arguments);
}
console.log("isArray:", typeCheck());            // true (用 Array 简化实现)

// 嵌套函数中 arguments 各自独立
function outer() {
  return function() {
    return arguments.length;
  }();
}
console.log("nested:", outer(1, 2));             // 0 (内层无参)
