let a = 5, b = 3;
let x = 10;
// 嵌套模板带表达式
console.log(`sum: ${`${a} + ${b} = ${a + b}`}`);
// 三重嵌套
console.log(`L1 ${`L2 ${`L3 ${x}`}`}`);
// 嵌套模板作为函数参数
function greet(name) { return `hi ${name}`; }
console.log(`${greet(`${greet("deep")}`)}`);
// 对象内嵌套模板
let obj = { val: `${`inner-${x}`}` };
console.log(obj.val);
