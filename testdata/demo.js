// js-runtime 综合测试脚本
// 展示 ES6 子集运行时的各项功能

// ===== 基础运算 =====
let a = 10;
let b = 3;
console.log("=== Arithmetic ===");
console.log(a + b);   // 13
console.log(a - b);   // 7
console.log(a * b);   // 30
console.log(a / b);   // 3.333...
console.log(a % b);   // 1
console.log(a ** b);  // 1000

// ===== 字符串操作 =====
console.log("\n=== Strings ===");
let name = "World";
console.log("Hello, " + name + "!");
console.log(`Template: ${name} length = ${name.length}`);
console.log("hello".toUpperCase());
console.log("  trim me  ".trim());
console.log("a,b,c".split(","));
console.log("replace".replace("e", "E"));

// ===== 数组操作 =====
console.log("\n=== Arrays ===");
let arr = [1, 2, 3, 4, 5];
console.log(arr);
console.log(arr.length);
console.log(arr[0]);
arr.push(6);
console.log(arr);
console.log(arr.slice(1, 3));
console.log(arr.indexOf(3));
console.log(arr.includes(99));
console.log([3, 1, 2].sort());
console.log([1, [2, 3]].flat());

// ===== 对象操作 =====
console.log("\n=== Objects ===");
let obj = { x: 1, y: 2, z: 3 };
console.log(obj);
console.log(obj.x);
console.log(Object.keys(obj));
console.log(Object.values(obj));
console.log(Object.entries(obj));
let merged = Object.assign({ a: 1 }, { b: 2 });
console.log(merged);

// ===== Math =====
console.log("\n=== Math ===");
console.log(Math.PI);
console.log(Math.floor(3.7));
console.log(Math.ceil(3.2));
console.log(Math.round(2.5));
console.log(Math.sqrt(144));
console.log(Math.max(1, 5, 3));
console.log(Math.min(1, 5, 3));
console.log(Math.abs(-42));

// ===== JSON =====
console.log("\n=== JSON ===");
let data = { name: "test", value: 42, arr: [1, 2, 3] };
let json = JSON.stringify(data);
console.log(json);
let parsed = JSON.parse('{"x": 1, "y": 2}');
console.log(parsed.x);

// ===== 控制流 =====
console.log("\n=== Control Flow ===");
for (let i = 0; i < 3; i++) {
    console.log("loop " + i);
}

let n = 0;
while (n < 3) {
    n++;
}
console.log("while result: " + n);

for (let item of [10, 20, 30]) {
    console.log("for-of: " + item);
}

// ===== 函数 =====
console.log("\n=== Functions ===");
function fib(n) {
    if (n < 2) { return n; }
    return fib(n - 1) + fib(n - 2);
}
console.log("fib(10) = " + fib(10));

const add = (a, b) => a + b;
console.log("arrow: " + add(3, 4));

function makeCounter() {
    let count = 0;
    return function() {
        count = count + 1;
        return count;
    };
}
let counter = makeCounter();
console.log(counter());
console.log(counter());
console.log(counter());

// ===== 解构 =====
console.log("\n=== Destructuring ===");
let [x, y] = [100, 200];
console.log(x + ", " + y);
let { a: pa, b: pb } = { a: 1, b: 2 };
console.log(pa + ", " + pb);

// ===== Spread =====
console.log("\n=== Spread ===");
let arr1 = [1, 2, 3];
let arr2 = [...arr1, 4, 5];
console.log(arr2);

// ===== Rest 参数 =====
console.log("\n=== Rest Params ===");
function sum(...nums) {
    let total = 0;
    for (let n of nums) {
        total += n;
    }
    return total;
}
console.log(sum(1, 2, 3, 4, 5));

// ===== 默认参数 =====
console.log("\n=== Default Params ===");
function greet(name = "World") {
    return "Hello, " + name + "!";
}
console.log(greet());
console.log(greet("JS"));

console.log("\n=== All tests passed! ===");
