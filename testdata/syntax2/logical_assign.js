// 逻辑赋值运算符测试
// ||= : 左侧为假值才赋值
let a = null;
a ||= "default";
console.log("a||=:", a);          // default
let b = "keep";
b ||= "changed";
console.log("b||=:", b);          // keep (短路径: 不计算 RHS)

// &&= : 左侧为真值才赋值
let c = 5;
c &&= 10;
console.log("c&&=:", c);          // 10
let d = 0;
d &&= 99;
console.log("d&&=:", d);          // 0 (短路径)

// ??= : 左侧为 nullish 才赋值
let e = null;
e ??= "set";
console.log("e??=:", e);          // set
let f = 42;
f ??= 7;
console.log("f??=:", f);          // 42 (短路径)
let g = 0;
g ??= 9;
console.log("g??=:", g);          // 0 (0 非 nullish, 不赋值)

// 对象属性
let obj = { x: 0, y: null };
obj.x ||= 100;
obj.y ||= 200;
console.log("obj.x||=:", obj.x);  // 100 (x=0 假值)
console.log("obj.y||=:", obj.y);  // 200 (y=null 假值)

let obj2 = { p: 5 };
obj2.p &&= 50;
console.log("obj2.p&&=:", obj2.p); // 50

let obj3 = {};
obj3.z ??= 30;
console.log("obj3.z??=:", obj3.z); // 30

// RHS 短路验证: 用函数副作用代替逗号运算符 (逗号运算符尚未支持)
let sideEffects = 0;
function bump() { sideEffects++; return "new"; }
let h = "truthy";
h ||= bump();
console.log("||= short-circuit sideEffects:", sideEffects); // 0

let i = 0;
i &&= bump();
console.log("&&= short-circuit sideEffects:", sideEffects); // 0

let j = 5;
j ??= bump();
console.log("??= short-circuit sideEffects:", sideEffects); // 0

// 短路时 RHS 应执行
let k = null;
k ||= bump();
console.log("||= executes RHS sideEffects:", sideEffects); // 1
