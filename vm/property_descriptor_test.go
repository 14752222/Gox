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
