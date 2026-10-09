package object

// ProtoOf 返回任意值的 [[Prototype]] (原型链上一环), 取不到时返回 nil。
//
// 这是原型链遍历的**收敛点**: instanceof 运算符 (vm.instanceOf) 与
// %Function.prototype%[@@hasInstance] (stdlib.ordinaryHasInstance) 都走
// 这里 —— 两处若各写一份类型穷举, 迟早会在某个类型上分叉, 出现
// `x instanceof C` 与 `C[Symbol.hasInstance](x)` 结论不一致的怪事。
//
// 此前该函数只存在于 vm 包 (vm.protoOf), stdlib 无从复用, 于是
// @@hasInstance 要么无法实现、要么只能另写一份残缺的遍历。
func ProtoOf(v Value) Value {
	switch t := v.(type) {
	case *Object:
		return t.Proto
	case *Array:
		return t.GetProto()
	// 函数对象的 [[Prototype]] 按种类指向内建原型 (Function/GeneratorFunction/
	// AsyncFunction/AsyncGeneratorFunction.prototype)，用于 instanceof 等沿链查找。
	case *Closure:
		return FuncPrototypeOf(t)
	case *BuiltinFunction:
		return FuncPrototypeOf(t)
	case *BuiltinMethod:
		return FuncPrototypeOf(t)
	// Temporal 类型把原型放在类型注册表里 (见 object.SetTemporalProto)，
	// 各自实现了 GetProto()。缺了这些分支，instanceof 会退化成名称匹配，
	// 而构造器名 ("Instant") 与类型标识并不对应。
	case *TemporalInstant:
		return t.GetProto()
	case *TemporalPlainDateTime:
		return t.GetProto()
	case *TemporalPlainDate:
		return t.GetProto()
	case *TemporalPlainTime:
		return t.GetProto()
	case *TemporalPlainYearMonth:
		return t.GetProto()
	case *TemporalPlainMonthDay:
		return t.GetProto()
	case *TemporalZonedDateTime:
		return t.GetProto()
	case *TemporalDuration:
		return t.GetProto()
	case *TemporalTimeZone:
		return t.GetProto()
	case *TemporalCalendar:
		return t.GetProto()
	// Date 的原型同样放在类型注册表里 (见 object.SetDateProto)。
	case *Date:
		return t.GetProto()
	// RegExp 的原型同样在类型注册表里 (object.RegExpProto, 见 rEXjyz)。
	case *RegExp:
		return t.GetProto()
	}
	return nil
}
