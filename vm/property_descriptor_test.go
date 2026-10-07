package vm

import "testing"

// ===== 属性描述符建模 (rmdv40 缺口 2 第一期) =====
//
// 此前 PropertyDescriptor 只携带 {Value, Writable}: Enumerable/Configurable
// 无处存放，于是 getOwnPropertyDescriptor 硬编码 enumerable=true/
// configurable=false，Object.keys / for-in / JSON.stringify 一律返回全部自有键，
// 可枚举性形同虚设；Object.defineProperty 也不做任何兼容性校验。
//
// 第一期给属性槽补上 Enumerable/Configurable，并让 Object.* API、枚举类
// 内建 (Object.keys/values/entries/assign/defineProperties)、for-in、
// JSON.stringify、Object.prototype.propertyIsEnumerable 全部尊重可枚举性。
//
// 期望值均以 Node 22 实测为准 (语义权威)。
//
// 说明: 对象字面量的 [[Prototype]] 目前尚未指向 Object.prototype ("{} .toString"
// 为 undefined，与本缺口无关的既有问题)，因此 propertyIsEnumerable 一律经
// Object.prototype.propertyIsEnumerable.call(...) 调用。

// 赋值创建的属性: writable/enumerable/configurable 全为 true。
func TestPropDescAssignmentDefaults(t *testing.T) {
	assertJS(t, `(function(){var o={};o.x=1;var d=Object.getOwnPropertyDescriptor(o,'x');
		return d.writable+'/'+d.enumerable+'/'+d.configurable;})()`, "true/true/true")
}

// Object.defineProperty 新建且缺省字段: 全为 false。
func TestPropDescDefineNewDefaults(t *testing.T) {
	assertJS(t, `(function(){var o={};Object.defineProperty(o,'y',{value:2});
		var d=Object.getOwnPropertyDescriptor(o,'y');
		return d.value+'/'+d.writable+'/'+d.enumerable+'/'+d.configurable;})()`, "2/false/false/false")
}

// defineProperty 更新已有属性: 缺省字段保留原值而非清零。
func TestPropDescDefineUpdateKeepsDefaults(t *testing.T) {
	assertJS(t, `(function(){var o={x:1};Object.defineProperty(o,'x',{value:5});
		var d=Object.getOwnPropertyDescriptor(o,'x');
		return d.value+'/'+d.writable+'/'+d.enumerable+'/'+d.configurable;})()`, "5/true/true/true")
}

// 不可配置属性的重定义约束 (ValidateAndApplyPropertyDescriptor)。
func TestPropDescNonConfigurableRedefine(t *testing.T) {
	// 改值 → TypeError
	assertJSThrows(t, `(function(){var o={};Object.defineProperty(o,'x',{value:1,configurable:false});
		Object.defineProperty(o,'x',{value:2});})()`, "TypeError")
	// 不可写 → 可写 → TypeError
	assertJSThrows(t, `(function(){var o={};Object.defineProperty(o,'x',{value:1,writable:false,configurable:false});
		Object.defineProperty(o,'x',{writable:true});})()`, "TypeError")
	// 改可枚举性 → TypeError
	assertJSThrows(t, `(function(){var o={};Object.defineProperty(o,'x',{value:1,configurable:false});
		Object.defineProperty(o,'x',{enumerable:true});})()`, "TypeError")
	// 同值重定义 → 允许 (不抛)
	assertJS(t, `(function(){var o={};Object.defineProperty(o,'x',{value:1,writable:false,configurable:false});
		Object.defineProperty(o,'x',{value:1});return 'ok';})()`, "ok")
}

// ToPropertyDescriptor: 非对象描述符 → TypeError。
func TestPropDescNonObjectDescriptorThrows(t *testing.T) {
	assertJSThrows(t, `Object.defineProperty({}, 'a', 1)`, "TypeError")
	assertJSThrows(t, `Object.defineProperty({}, 'a', null)`, "TypeError")
	assertJSThrows(t, `Object.defineProperty({}, 'a', undefined)`, "TypeError")
}

// 可枚举性驱动枚举类 API: Object.keys / JSON.stringify / for-in 一致隐藏不可枚举属性。
func TestPropDescEnumerableAffectsEnumeration(t *testing.T) {
	const setup = `var o={a:1};Object.defineProperty(o,'b',{value:2,enumerable:false});`
	assertJS(t, `(function(){`+setup+`return JSON.stringify(Object.keys(o));})()`, `["a"]`)
	assertJS(t, `(function(){`+setup+`return JSON.stringify(o);})()`, `{"a":1}`)
	assertJS(t, `(function(){`+setup+`var r=[];for(var k in o)r.push(k);return JSON.stringify(r);})()`, `["a"]`)
	// getOwnPropertyNames 仍返回全部自有键 (含不可枚举)。
	assertJS(t, `(function(){`+setup+`return JSON.stringify(Object.getOwnPropertyNames(o));})()`, `["a","b"]`)
}

// propertyIsEnumerable: 反映真实可枚举性。
func TestPropDescPropertyIsEnumerable(t *testing.T) {
	assertJS(t, `Object.prototype.propertyIsEnumerable.call({b:2},'b')`, "true")
	assertJS(t, `(function(){var o={};Object.defineProperty(o,'a',{value:1,enumerable:false});
		return Object.prototype.propertyIsEnumerable.call(o,'a');})()`, "false")
	assertJS(t, `Object.prototype.propertyIsEnumerable.call({},'zzz')`, "false")
	assertJS(t, `Object.prototype.propertyIsEnumerable.call([1,2],'0')`, "true")
}

// Object.freeze: 不可扩展 + 所有自有数据属性 writable/configurable=false。
func TestPropDescFreeze(t *testing.T) {
	assertJS(t, `(function(){var o={a:1};Object.freeze(o);var d=Object.getOwnPropertyDescriptor(o,'a');
		return d.writable+'/'+d.configurable+'/'+Object.isFrozen(o)+'/'+Object.isSealed(o);})()`, "false/false/true/true")
	// freeze 后赋值静默失败 (非严格模式)。
	assertJS(t, `(function(){var o={a:1};Object.freeze(o);o.a=9;return o.a;})()`, "1")
}

// Object.seal: 不可扩展 + configurable=false, 但保持可写。
func TestPropDescSeal(t *testing.T) {
	assertJS(t, `(function(){var o={a:1};Object.seal(o);var d=Object.getOwnPropertyDescriptor(o,'a');
		return d.writable+'/'+d.configurable+'/'+Object.isFrozen(o)+'/'+Object.isSealed(o);})()`, "true/false/false/true")
	assertJS(t, `(function(){var o={a:1};Object.seal(o);o.a=9;return o.a;})()`, "9")
}

// 删除不可配置属性失败。
func TestPropDescDeleteNonConfigurable(t *testing.T) {
	assertJS(t, `(function(){var o={};Object.defineProperty(o,'a',{value:1,configurable:false});
		var r=(delete o.a);return r+'/'+('a' in o);})()`, "false/true")
}

// 访问器描述符: 新建缺省 enumerable/configurable 为 false。
func TestPropDescAccessorDefaults(t *testing.T) {
	assertJS(t, `(function(){var o={};Object.defineProperty(o,'a',{get:function(){return 1;}});
		var d=Object.getOwnPropertyDescriptor(o,'a');
		return (typeof d.get)+'/'+(typeof d.set)+'/'+d.enumerable+'/'+d.configurable;})()`,
		"function/undefined/false/false")
	// 数据 → 访问器切换 (原属性可配置)。
	assertJS(t, `(function(){var o={a:1};Object.defineProperty(o,'a',{get:function(){return 9;}});
		return o.a+'/'+(Object.getOwnPropertyDescriptor(o,'a').get!==undefined);})()`, "9/true")
}

// Object.defineProperties 只处理描述符对象上的可枚举自有属性。
func TestPropDescDefinePropertiesEnumerableOnly(t *testing.T) {
	assertJS(t, `(function(){var o={};var d={a:{value:1}};
		Object.defineProperty(d,'b',{value:{value:2},enumerable:false});
		Object.defineProperties(o,d);
		return JSON.stringify(Object.getOwnPropertyNames(o));})()`, `["a"]`)
}

// Object.getOwnPropertyDescriptors 反映真实 enumerable/configurable。
func TestPropDescGetOwnPropertyDescriptors(t *testing.T) {
	assertJS(t, `(function(){var o={a:1};Object.defineProperty(o,'b',{value:2,enumerable:false});
		var ds=Object.getOwnPropertyDescriptors(o);
		return ds.b.enumerable+'/'+ds.b.configurable+'/'+ds.a.enumerable+'/'+ds.a.configurable;})()`,
		"false/false/true/true")
}

// ===== 看板 rxsCia: 函数对象的自有可枚举属性 =====
//
// 函数是独立类型 (不是 *object.Object), 此前 Object.keys(fn) 恒为 []。
// 规范: 函数上普通赋值 (f.custom = 1) 是 { writable:true, enumerable:true,
// configurable:true } 的自有数据属性, 必须被 Object.keys / for-in /
// JSON.stringify / Object.assign / 展开 / getOwnPropertyNames 列出;
// 而结构性 name/length/prototype 与内置/class 静态方法/访问器不可枚举。
// 期望值以 Node 22 实测为准。

// 函数上的普通赋值: Object.keys 列出, 描述符 enumerable:true。
func TestFunctionOwnAssignEnumerable(t *testing.T) {
	assertJS(t, `(function(){function f(){} f.custom=1; return JSON.stringify(Object.keys(f));})()`, `["custom"]`)
	assertJS(t, `(function(){function f(){} f.a=1; f.b=2; return JSON.stringify(Object.keys(f));})()`, `["a","b"]`)
	assertJS(t, `(function(){function f(){} f.custom=1;
		var d=Object.getOwnPropertyDescriptor(f,'custom');
		return d.value+'/'+d.writable+'/'+d.enumerable+'/'+d.configurable;})()`, "1/true/true/true")
}

// 箭头函数与赋值的可枚举性一致。
func TestArrowFunctionOwnAssignEnumerable(t *testing.T) {
	assertJS(t, `(function(){const g=()=>{}; g.z=3; return JSON.stringify(Object.keys(g));})()`, `["z"]`)
}

// Object.values / entries / assign / 展开 / for-in / JSON.stringify 一致。
//
// 函数自有键按规范的 OrdinaryOwnPropertyKeys 输出 (整数键升序 → 字符串键
// 插入序, 见 rS4HXt)。此处 f.custom 先于 f.b 赋值, 故顺序为 custom, b ——
// 与普通对象同口径 (不再按名字典序)。
func TestFunctionOwnAssignConsumers(t *testing.T) {
	const setup = `function f(){} f.custom=1; f.b=2;`
	assertJS(t, `(function(){`+setup+`return JSON.stringify(Object.values(f));})()`, `[1,2]`)
	assertJS(t, `(function(){`+setup+`return JSON.stringify(Object.entries(f));})()`, `[["custom",1],["b",2]]`)
	assertJS(t, `(function(){`+setup+`return JSON.stringify(Object.assign({},f));})()`, `{"custom":1,"b":2}`)
	assertJS(t, `(function(){`+setup+`return JSON.stringify({...f});})()`, `{"custom":1,"b":2}`)
	assertJS(t, `(function(){`+setup+`var r=[];for(var k in f)r.push(k);return JSON.stringify(r.sort());})()`, `["b","custom"]`)
}

// getOwnPropertyNames 含全部自有键 (name/length/prototype + 赋值属性)。
func TestFunctionGetOwnPropertyNames(t *testing.T) {
	assertJS(t, `(function(){function f(){} f.custom=1;
		return JSON.stringify(Object.getOwnPropertyNames(f));})()`, `["length","name","prototype","custom"]`)
	// 结构性 name/length/prototype 不可枚举, 不出现在 keys 里。
	assertJS(t, `(function(){function f(){}
		return JSON.stringify(Object.keys(f));})()`, `[]`)
}

// 内置函数的静态成员 (String.fromCharCode 等) 仍不可枚举。
func TestBuiltinFunctionStaticsNonEnumerable(t *testing.T) {
	assertJS(t, `JSON.stringify(Object.keys(String))`, `[]`)
	assertJS(t, `JSON.stringify(Object.keys(Array))`, `[]`)
	assertJS(t, `JSON.stringify(Object.keys(Object))`, `[]`)
	// 但用户新增的静态属性可枚举。
	assertJS(t, `(function(){String.qq=5; var r=JSON.stringify(Object.keys(String)); delete String.qq; return r;})()`, `["qq"]`)
}

// class 静态方法/访问器不可枚举, class 静态字段可枚举。
func TestClassStaticMemberEnumerable(t *testing.T) {
	assertJS(t, `(function(){class C{static m(){} static f=5; static get g(){return 1}}
		return JSON.stringify(Object.keys(C));})()`, `["f"]`)
	assertJS(t, `(function(){class C{static m(){}}
		return Object.getOwnPropertyDescriptor(C,'m').enumerable;})()`, "false")
	assertJS(t, `(function(){class C{static f=5;}
		return Object.getOwnPropertyDescriptor(C,'f').enumerable;})()`, "true")
}

// 数组 Object.keys 行为保持不变 (回归护栏): 只列可枚举索引, 不含 length。
func TestArrayKeysUnaffected(t *testing.T) {
	assertJS(t, `JSON.stringify(Object.keys([1,2,3]))`, `["0","1","2"]`)
	assertJS(t, `JSON.stringify(Object.getOwnPropertyNames([1,2]))`, `["0","1","length"]`)
	assertJS(t, `JSON.stringify(Object.keys([]))`, `[]`)
}
