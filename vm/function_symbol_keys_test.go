package vm

import "testing"

// rS4HXt 回归测试: 函数对象 (*Closure / *BuiltinFunction / *BuiltinMethod) 的
// Symbol 键自有属性枚举, 以及函数自有键的 OrdinaryOwnPropertyKeys 顺序
// (整数键升序 → 字符串键插入序 → Symbol 键插入序)。

// symJoin 是测试内的公共片段: 把 Symbol 键数组转成可比较字符串。
const symJoin = `.map(function(x){return x.toString();}).join(',')`

// --- ① Symbol 键属性: 存得进 / 读得回 / 枚举得出 ---

func TestFunctionSymbolKeyStoreRead(t *testing.T) {
	// 普通函数
	assertJS(t, `(function(){var s=Symbol('x');var f=function(){};f[s]=1;return f[s];})()`, "1")
	// 箭头函数
	assertJS(t, `(function(){var s=Symbol('x');var g=()=>{};g[s]=5;return g[s];})()`, "5")
	// 类构造器 (静态 Symbol 属性)
	assertJS(t, `(function(){var s=Symbol('x');class C{};C[s]=9;return C[s];})()`, "9")
	// 内建函数 (Array.prototype.map 是 *BuiltinMethod)
	assertJS(t, `(function(){var s=Symbol('x');var b=Array.prototype.map;b[s]=42;return b[s];})()`, "42")
}

func TestFunctionGetOwnPropertySymbols(t *testing.T) {
	assertJS(t, `(function(){var s=Symbol('x');var f=function(){};f[s]=1;
		return Object.getOwnPropertySymbols(f)`+symJoin+`;})()`, "Symbol(x)")
	// 内建函数
	assertJS(t, `(function(){var s=Symbol('x');var b=Array.prototype.map;b[s]=42;
		return Object.getOwnPropertySymbols(b)`+symJoin+`;})()`, "Symbol(x)")
	// 未设置时为空
	assertJS(t, `Object.getOwnPropertySymbols(function(){})`+symJoin, "")
}

func TestFunctionSymbolKeyInReflectOwnKeys(t *testing.T) {
	// Symbol 键排在字符串键之后 (OrdinaryOwnPropertyKeys 末段)。
	assertJS(t, `(function(){var s=Symbol('x');var f=function(){};f[s]=1;
		return Reflect.ownKeys(f).map(function(k){return typeof k==='symbol'?k.toString():k;}).join(',');})()`,
		"length,name,prototype,Symbol(x)")
	// 纯函数 (无自定义键) 的 ownKeys
	assertJS(t, `Reflect.ownKeys(function(){}).join(',')`, "length,name,prototype")
}

func TestFunctionSymbolKeyDescriptor(t *testing.T) {
	assertJS(t, `(function(){var s=Symbol('x');var f=function(){};f[s]=1;
		var d=Object.getOwnPropertyDescriptor(f,s);
		return d.value+'/'+d.writable+'/'+d.enumerable+'/'+d.configurable;})()`, "1/true/true/true")
	assertJS(t, `(function(){var s=Symbol('x');var f=function(){};
		Object.defineProperty(f,s,{value:7,enumerable:true,configurable:true,writable:true});
		var d=Object.getOwnPropertyDescriptor(f,s);
		return d.value+'/'+d.writable+'/'+d.enumerable+'/'+d.configurable;})()`, "7/true/true/true")
	// 不存在的 Symbol 键
	assertJS(t, `String(Object.getOwnPropertyDescriptor(function(){},Symbol('nope')))`, "undefined")
}

func TestFunctionSymbolKeyHasOwnProperty(t *testing.T) {
	assertJS(t, `(function(){var s=Symbol('x');var f=function(){};f[s]=1;
		return f.hasOwnProperty(s);})()`, "true")
	assertJS(t, `(function(){var s=Symbol('x');var f=function(){};f[s]=1;
		return f.propertyIsEnumerable(s);})()`, "true")
	// 不可枚举 Symbol 键: getOwnPropertySymbols 仍列出 (规范), propertyIsEnumerable 为 false。
	assertJS(t, `(function(){var s=Symbol('x');var f=function(){};
		Object.defineProperty(f,s,{value:1,enumerable:false});
		return Object.getOwnPropertySymbols(f).length+'/'+f.propertyIsEnumerable(s);})()`, "1/false")
	// 内建函数
	assertJS(t, `(function(){var s=Symbol('x');var b=Array.prototype.map;b[s]=1;
		return b.hasOwnProperty(s);})()`, "true")
}

func TestFunctionSymbolAccessor(t *testing.T) {
	assertJS(t, `(function(){var s=Symbol('x');var f=function(){};
		Object.defineProperty(f,s,{get:function(){return 3;},enumerable:true,configurable:true});
		var d=Object.getOwnPropertyDescriptor(f,s);
		return Object.getOwnPropertySymbols(f)`+symJoin+`+'/'+f[s]+'/'+(typeof d.get);})()`,
		"Symbol(x)/3/function")
}

// --- ② 函数自有键顺序: OrdinaryOwnPropertyKeys ---

func TestFunctionOwnKeyOrder(t *testing.T) {
	// 卡片指定用例: 整数键升序 → 字符串键插入序。
	assertJS(t, `(function(){var f=()=>{};f.b=1;f.a=2;f[2]=3;f[1]=4;f.c=5;
		return Object.keys(f).join(',');})()`, "1,2,b,a,c")
	// getOwnPropertyNames 含结构性 length/name (箭头函数无 prototype)。
	assertJS(t, `(function(){var f=()=>{};f.b=1;f.a=2;f[2]=3;f[1]=4;f.c=5;
		return Object.getOwnPropertyNames(f).join(',');})()`, "1,2,length,name,b,a,c")
	// for-in 同序 (仅可枚举键)。
	assertJS(t, `(function(){var f=()=>{};f.b=1;f.a=2;f[2]=3;f[1]=4;f.c=5;
		var r=[];for(var k in f)r.push(k);return r.join(',');})()`, "1,2,b,a,c")
	// Symbol 键按插入序排在字符串键之后。
	assertJS(t, `(function(){var s=Symbol('x'),s2=Symbol('y');var f=function(){};
		f.b=1;f[s]=2;f.a=3;f[s2]=4;
		return Reflect.ownKeys(f).map(function(k){return typeof k==='symbol'?k.toString():k;}).join(',');})()`,
		"length,name,prototype,b,a,Symbol(x),Symbol(y)")
	// delete 后重加: 回到末尾。
	assertJS(t, `(function(){var f=function(){};f.a=1;f.b=2;delete f.a;f.a=3;
		return Object.keys(f).join(',');})()`, "b,a")
	// 普通函数 (有 prototype) 的完整自有键序。
	assertJS(t, `(function(){function f(){}f.custom=1;
		return Object.getOwnPropertyNames(f).join(',');})()`, "length,name,prototype,custom")
}

func TestFunctionIntegerKeyStored(t *testing.T) {
	// f[1] === f["1"] 且落在自有属性上 (此前整数字面量键被静默丢弃)。
	assertJS(t, `(function(){var f=()=>{};f[1]=4;return f[1]+'/'+f['1']+'/'+f.hasOwnProperty('1');})()`, "4/4/true")
	assertJS(t, `(function(){var f=()=>{};f[1]=4;return Object.keys(f).join(',');})()`, "1")
	assertJS(t, `(function(){var f=()=>{};f[10]=1;f[2]=2;
		return Object.getOwnPropertyNames(f).join(',');})()`, "2,10,length,name")
}

// --- ③ 反向断言: Symbol 键不进字符串枚举通道 ---

func TestFunctionSymbolKeyNotInStringEnumeration(t *testing.T) {
	assertJS(t, `(function(){var s=Symbol('x');var f=function(){};f[s]=1;f.a=2;
		var r=[];for(var k in f)r.push(k);return r.join(',');})()`, "a")
	assertJS(t, `(function(){var s=Symbol('x');var f=function(){};f[s]=1;
		return Object.keys(f).length;})()`, "0")
	assertJS(t, `(function(){var s=Symbol('x');var f=function(){};f[s]=1;
		return Object.getOwnPropertyNames(f).indexOf('x') === -1;})()`, "true")
	// String(sym) / description 不受影响。
	assertJS(t, `(function(){var s=Symbol('x');return String(s)+'/'+s.description;})()`, "Symbol(x)/x")
}

// --- ④ 数组/内建函数侧不回归 ---

func TestArrayAndBuiltinFunctionKeyRegressions(t *testing.T) {
	// 数组 Object.keys 只列可枚举索引。
	assertJS(t, `Object.keys([1,2,3]).join(',')`, "0,1,2")
	// 数组自有键顺序: 索引升序 → length。
	assertJS(t, `Reflect.ownKeys([1,2,3]).join(',')`, "0,1,2,length")
	// 数组 Symbol 键仍可存可枚举。
	assertJS(t, `(function(){var s=Symbol('x');var a=[];a[s]=1;
		return a[s]+'/'+Object.getOwnPropertySymbols(a)`+symJoin+`;})()`, "1/Symbol(x)")
	// 内建函数静态成员仍不可枚举; 用户新增的仍可枚举。
	assertJS(t, `Object.keys(String).join(',')`, "")
	assertJS(t, `(function(){String.qq=5;var r=Object.keys(String).join(',');delete String.qq;return r;})()`, "qq")
	// *BuiltinFunction.prototype 是 Properties 里的普通自有键, 必须列出。
	assertJS(t, `Object.getOwnPropertyNames(Object).indexOf('prototype') > -1`, "true")
	assertJS(t, `Object.prototype.hasOwnProperty('constructor')`, "true")
}
