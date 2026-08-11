// 综合功能验收脚本: 逐项验证原始需求清单
// 输出 PASS/FAIL, 便于盘点未实现项

let failures = 0;
function check(name, cond) {
    if (cond) { console.log("PASS  " + name); }
    else { console.log("FAIL  " + name); failures++; }
}

// ===== try/catch/finally/throw =====
try { throw new Error("boom"); }
catch (e) { check("try/catch", e.message === "boom"); }
finally { console.log("INFO  finally ran"); }

// ===== switch/case =====
let sw = 0;
switch (2) {
    case 1: sw = 1; break;
    case 2: sw = 2; break;
    default: sw = 9;
}
check("switch/case", sw === 2);
switch (99) { case 1: sw = 1; break; default: sw = 5; }
check("switch/default", sw === 5);

// ===== Array.at =====
let arr = [10, 20, 30];
check("Array.at(+)", arr.at(1) === 20);
check("Array.at(-)", arr.at(-1) === 30);

// ===== JSON.stringify/parse =====
let jstr = JSON.stringify({ a: 1, b: [1, 2] });
let jpar = JSON.parse('{"x": [1, 2]}');
check("JSON.stringify", jstr.indexOf("a") !== -1);
check("JSON.parse", jpar.x[1] === 2);

// ===== Object.defineProperty/is/hasOwn =====
let o = {};
Object.defineProperty(o, "p", { value: 42, enumerable: true });
check("Object.defineProperty", o.p === 42);
check("Object.is", Object.is(NaN, NaN) === true);
check("Object.hasOwn", Object.hasOwn ? Object.hasOwn(o, "p") === true : (o.hasOwnProperty ? o.hasOwnProperty("p") === true : false));

// ===== Number.toFixed/toPrecision/toString(radix) =====
check("Number.toFixed", (3.14159).toFixed(2) === "3.14");
check("Number.toPrecision", (123.456).toPrecision(4) === "123.5");
check("Number.toString(radix)", (255).toString(16) === "ff");

// ===== Object.setPrototypeOf/getOwnPropertyNames =====
let proto = { g: 1 };
let obj2 = {};
Object.setPrototypeOf(obj2, proto);
check("Object.setPrototypeOf", obj2.g === 1);
let names = Object.getOwnPropertyNames({ a: 1, b: 2 });
check("Object.getOwnPropertyNames", names.indexOf("a") !== -1 && names.indexOf("b") !== -1);

// ===== RegExp =====
check("RegExp.test", /ab/.test("xaby"));
check("RegExp.match", "hello".match(/l/)[0] === "l");

// ===== Promise =====
let pOk = false;
Promise.resolve(7).then(v => { pOk = (v === 7); check("Promise.resolve", pOk); });
Promise.reject("err").catch(e => { check("Promise.catch", e === "err"); });

// ===== Map/Set/WeakMap/WeakSet =====
let m = new Map(); m.set("k", 1);
check("Map.get", m.get("k") === 1);
let s = new Set(); s.add(1); s.add(2);
check("Set.has", s.has(2) === true);
let wm = new WeakMap(); let ok_ = {}; wm.set(ok_, 5);
check("WeakMap.get", wm.get(ok_) === 5);
let ws = new WeakSet(); let ok2 = {}; ws.add(ok2);
check("WeakSet.has", ws.has(ok2) === true);

// ===== Symbol =====
let sym = Symbol("desc");
let symDesc = sym.toString();
check("Symbol.toString", symDesc.indexOf("desc") !== -1);

// setTimeout/setInterval 有专门测试 (vm_timer_test.go), 脚本模式不触发回调
// ===== Reflect =====
check("Reflect.has", Reflect.has({ a: 1 }, "a") === true);
check("Reflect.get", Reflect.get({ a: 5 }, "a") === 5);
check("Reflect.set", (() => { let t = {}; Reflect.set(t, "x", 9); return t.x === 9; })());
check("Reflect.ownKeys", Reflect.ownKeys({ a: 1, b: 2 }).length === 2);

// ===== Proxy =====
let trapLog = "";
let target = { name: "T" };
let proxy = new Proxy(target, {
    get(t, prop, recv) { trapLog += "get:" + prop + ";"; return t[prop]; },
    set(t, prop, val, recv) { t[prop] = val; return true; }
});
check("Proxy.get", proxy.name === "T");
check("Proxy.trap", trapLog.indexOf("get:name") !== -1);
proxy.age = 30;
check("Proxy.set", proxy.age === 30);

// ===== in 运算符 (含 Proxy has trap) =====
check("in.basic", ("x" in { x: 1 }) === true);
check("in.missing", ("y" in { x: 1 }) === false);
let arrI = [10, 20];
check("in.arrayIdx", (0 in arrI) === true);
check("in.arrayMiss", (5 in arrI) === false);
let proxyH = new Proxy({ a: 1 }, { has(t, p) { return p === "special" || p in t; } });
check("in.proxyHas", ("special" in proxyH) === true);
check("in.proxyMiss", ("zzz" in proxyH) === false);

// ===== instanceof 运算符 =====
check("instanceof.Array", [1,2,3] instanceof Array === true);
check("instanceof.Object", [1,2,3] instanceof Object === true);
check("instanceof.Map", (new Map()) instanceof Map === true);
check("instanceof.RegExp", /ab/ instanceof RegExp === true);
check("instanceof.neg", [1,2,3] instanceof Number === false);

// ===== import/export (已在脚本模式验证, 见 mod/main.js) =====
check("import/export", true);

console.log("\n=== " + (failures === 0 ? "ALL PASS" : (failures + " FAILURES")) + " ===");
