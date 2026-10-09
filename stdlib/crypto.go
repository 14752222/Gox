// gx/crypto: 摘要与随机数的**最小可用集** (看板 ru3TZK)。
//
//	hash("gox");                                  // 同步, 返回小写 hex
//	hash("gox", "SHA-512");                       // 算法名大小写/连字符不敏感
//	await crypto.subtle.digest("SHA-256", buf);   // Promise → ArrayBuffer
//	crypto.randomUUID();                          // RFC 4122 v4
//	crypto.getRandomValues(new Uint8Array(16));   // 原地填充
//
// 为什么这份能力必须存在 (看板原文): 此前 stdlib/ 全域没有一处把摘要/随机数
// 暴露给 JS (仅 update_module.go 里有一个 sha256 **字段**, 是 Go 侧自更新校验
// 用的, 不在 JS 可见面)。于是:
//   - apps #10 局域网快传只能"局域网明文 + 一次性 token";
//   - 密码本 / 端到端加密类应用被直接排除;
//   - apps #6 重复文件查找只能退化成"大小 + 前 N 字节"比对。
//
// 边界 (刻意做小, 一次交付可评审):
//   - 只做**摘要**与**随机数**, 不做加解密/签名/KDF —— 后者需要密钥管理与
//     算法选择上的产品设计, 不是一个"补个 API"就能定的事;
//   - 算法走 Go 标准库 crypto/*, 零 cgo、零第三方依赖, 不破约束;
//   - 同步 hash() 接受 MD5, crypto.subtle.digest() **不接受** (见 cryptoDigestAlgo
//     的 subtle 字段注释)。
//
// 双形态与 fs/http/process 同一口径: 实现的唯一真源是内置模块 "gx/crypto" 的
// 导出表, 全局 crypto 与全局 hash 只是复用同一批函数对象 —— 于是
// `import { hash } from "gx/crypto"` 与全局 hash 不会漂成两份实现。
package stdlib

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"sort"
	"strings"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

// cryptoDefaultAlgo 是 hash(data) 省略算法名时的默认值。
//
// 选 SHA-256: 它是"不知道该选什么"时的行业默认 (证书指纹、包校验、ETag),
// 且不是已知被攻破的算法。
const cryptoDefaultAlgo = "SHA-256"

// cryptoRandomValuesMaxBytes 是 WebCrypto 规定的单次 getRandomValues 上限
// (64KB)。超出抛 QuotaExceededError —— 与浏览器同口径, 免得脚本在别处能跑、
// 在这里静默拿到一串"看起来随机"的字节。
const cryptoRandomValuesMaxBytes = 65536

// cryptoDigestAlgo 描述一种受支持的摘要算法。
type cryptoDigestAlgo struct {
	name string // 规范化名, 报错信息里回显用 ("SHA-256")
	new  func() hash.Hash
	// subtle 为 false 时该算法只出现在同步 hash() 里。
	//
	// 为什么 MD5 不同时进 subtle: WebCrypto 的 SubtleCrypto.digest 只认
	// SHA-1 / SHA-256 / SHA-384 / SHA-512, 从来没有 MD5 —— 把它塞进
	// crypto.subtle.digest 等于承诺了一个浏览器里不存在的算法, 抄过来的
	// 代码在 Gox 上跑得通、上浏览器就挂。MD5 只留给 hash(): 它的真实用途
	// 是校验和/旧系统对账 (ETag、分块指纹), 不是安全。
	subtle bool
}

// cryptoDigestAlgos 的键是**规范化后**的算法名 (大写、无分隔符)。
var cryptoDigestAlgos = map[string]cryptoDigestAlgo{
	"SHA1":   {"SHA-1", sha1.New, true},
	"SHA256": {"SHA-256", sha256.New, true},
	"SHA512": {"SHA-512", sha512.New, true},
	"MD5":    {"MD5", md5.New, false},
}

// normalizeDigestAlgo 把算法名规范成查表键。
//
// WebCrypto 的算法名是**大小写不敏感**的 ("SHA-256" / "sha256" / "Sha_256"
// 同一个算法), 且连字符、下划线都只是书写风格 —— 一律剥掉再比对, 抄浏览器
// 侧代码不需要先做字符串体操。
func normalizeDigestAlgo(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '-', '_', ' ':
			// 分隔符: 剥掉
		default:
			b.WriteRune(r)
		}
	}
	return strings.ToUpper(b.String())
}

// cryptoSupportedNames 列出受支持的算法名, 用于报错信息 ("支持 SHA-1, ...")。
// subtleOnly 为真时只列 WebCrypto 认可的那些。
func cryptoSupportedNames(subtleOnly bool) string {
	names := make([]string, 0, len(cryptoDigestAlgos))
	for _, a := range cryptoDigestAlgos {
		if subtleOnly && !a.subtle {
			continue
		}
		names = append(names, a.name)
	}
	// map 迭代顺序随机, 排序后报错文本才可预期 (测试也才能断言)。
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// cryptoResolveAlgo 解析算法名参数; 该参数缺省时按 cryptoDefaultAlgo 走。
// subtle 为真时额外要求算法属于 WebCrypto 名单。
//
// 返回 (算法, nil) 或 (零值, *object.Error) —— 后者由调用方抛出或用于 reject。
func cryptoResolveAlgo(args []object.Value, idx int, op string, subtle bool) (cryptoDigestAlgo, object.Value) {
	name := cryptoDefaultAlgo
	if idx < len(args) && !isUndefinedValue(args[idx]) && !isNullValue(args[idx]) {
		s, ok := args[idx].(*object.String)
		if !ok {
			return cryptoDigestAlgo{}, object.NewTypeError(
				"%s: 算法名必须是字符串, 收到 %s", op, object.TypeOf(args[idx]))
		}
		name = s.Value
	}
	a, ok := cryptoDigestAlgos[normalizeDigestAlgo(name)]
	if !ok || (subtle && !a.subtle) {
		return cryptoDigestAlgo{}, object.NewTypeError(
			"%s: 不支持的摘要算法 %q (支持 %s)", op, name, cryptoSupportedNames(subtle))
	}
	return a, nil
}

// cryptoDataBytes 把 JS 值转成待摘要的字节。
//
// 接受的形态刻意有四种 —— apps 侧的数据来源本就不止一种, 只认 string 会逼着
// 每个调用方自己写一遍转换 (而转换写错 = 摘要算错, 不报错):
//   - string: 按 UTF-8 编码 (与 TextEncoder.encode 同口径);
//   - ArrayBuffer: 整块;
//   - TypedArray (Uint8Array 等): **视图覆盖的那段原始字节** (含 byteOffset,
//     多字节视图按元素宽度取满, 不做数字→字节的语义转换);
//   - 数字数组: Gox 自己的"字节数组等价物" —— fs.readBytesSync 返回的就是这个
//     形状, 于是 `hash(fs.readBytesSync(p))` 可以直接写 (apps #6 重复文件查找
//     正是这条路径)。
//
// 返回的切片**可能直接别名**到底层 ArrayBuffer: 调用方只能读, 不能改。
func cryptoDataBytes(op string, v object.Value) ([]byte, object.Value) {
	switch t := v.(type) {
	case *object.String:
		return []byte(t.Value), nil
	case *object.ArrayBuffer:
		if t.Detach {
			return nil, object.NewTypeError("%s: ArrayBuffer 已分离 (detached)", op)
		}
		return t.Data, nil
	case *object.TypedArray:
		if t.Buffer == nil || t.Buffer.Detach {
			return nil, object.NewTypeError("%s: TypedArray 的 buffer 已分离 (detached)", op)
		}
		start := t.ByteOffset
		end := start + t.Length*t.Kind.ElemSize
		if start < 0 {
			start = 0
		}
		if end > len(t.Buffer.Data) {
			end = len(t.Buffer.Data)
		}
		if end < start {
			end = start
		}
		return t.Buffer.Data[start:end], nil
	case *object.Array:
		return fsArrayToBytes(t), nil
	}
	return nil, object.NewTypeError(
		"%s: data 必须是 string / ArrayBuffer / TypedArray / 字节数组, 收到 %s",
		op, object.TypeOf(v))
}

// cryptoSum 计算摘要; 与具体 API 形态无关的唯一计算入口。
func cryptoSum(a cryptoDigestAlgo, data []byte) []byte {
	h := a.new()
	h.Write(data)
	return h.Sum(nil)
}

// jsHash 实现同步 hash(data, algo?) —— 返回**小写 hex** 字符串。
//
// 返回 hex 而不是字节: 摘要的绝大多数用途是比较与展示 (文件指纹、ETag、
// 去重键、日志), hex 直接可打印可比较; 需要字节时调用方自己 hex 解回来即可
// (fs.writeFileSync 认 "hex" 编码)。
func jsHash(args ...object.Value) object.Value {
	if len(args) < 1 {
		return object.NewTypeError("hash: 缺少 data 参数")
	}
	algo, errVal := cryptoResolveAlgo(args, 1, "hash", false)
	if errVal != nil {
		return errVal
	}
	data, errVal := cryptoDataBytes("hash", args[0])
	if errVal != nil {
		return errVal
	}
	return object.NewString(hex.EncodeToString(cryptoSum(algo, data)))
}

// jsDigest 实现 crypto.subtle.digest(algo, data) —— Promise<ArrayBuffer>。
//
// 参数序与 WebCrypto 一致 (**算法在前**), 与同步 hash(data, algo) 相反 ——
// 这不是笔误: hash 是 Gox 自己的糖, digest 是抄浏览器, 各自保持各处的习惯,
// 代价是两者顺序不同, 已在注释与 README 里写明。
//
// ArrayBuffer / TypedArray 在 Gox 里**已实现** (stdlib/typedarray.go), 所以
// 这里按规范 resolve 成 ArrayBuffer, 不做"降级成 hex 字符串"那类妥协;
// 要 hex 就调 hash()。
//
// 错误走 reject 而不是同步抛: 规范里算法归一化发生在 Promise 内部, 且
// `await` 是最自然的用法 —— try/catch 能接住, 与浏览器同形。
func jsDigest(args ...object.Value) object.Value {
	if len(args) < 2 {
		return rejectedDigest(object.NewTypeError("crypto.subtle.digest: 需要 (algo, data) 两个参数"))
	}
	algo, errVal := cryptoResolveAlgo(args, 0, "crypto.subtle.digest", true)
	if errVal != nil {
		return rejectedDigest(errVal)
	}
	data, errVal := cryptoDataBytes("crypto.subtle.digest", args[1])
	if errVal != nil {
		return rejectedDigest(errVal)
	}

	// 与 fsSchedule 同一模型: 工作注册成 0ms 定时器, 在事件循环主线程的下一个
	// tick 执行, Promise 在此之前保持 pending —— 于是 await 是真的让出了一次
	// 事件循环, 而不是"看起来异步、实则同步算完"的假 Promise。
	result := object.NewPromise()
	object.GlobalScheduler().SetTimeout(object.NewBuiltin("__crypto_digest_task", func(...object.Value) object.Value {
		sum := cryptoSum(algo, data)
		out := object.NewArrayBuffer(len(sum))
		copy(out.Data, sum)
		result.Resolve(out)
		// Resolve 会同步驱动 await 恢复链; 恢复链里的未捕获异常在这里消费掉,
		// 避免残留到之后的内建函数调用点被误抛出 (rr1O8P: 两槽必须一起消费)。
		if cbErr := object.TakeCallbackError(); cbErr != nil {
			reportUncaught(cbErr)
		}
		return object.UndefinedSingleton
	}), 0)
	return result
}

// rejectedDigest 造一个**已 reject** 的 Promise, 供参数校验失败的分支返回。
func rejectedDigest(reason object.Value) object.Value {
	p := object.NewPromise()
	p.Reject(reason)
	return p
}

// jsRandomUUID 实现 crypto.randomUUID() —— RFC 4122 v4 的 UUID 字符串。
// 随机源是 crypto/rand (OS 熵池), 不是 math/rand。
func jsRandomUUID(args ...object.Value) object.Value {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 读不到熵不该静默返回一个可预测的 UUID —— 那是"看起来随机"的安全洞。
		return object.NewError(fmt.Sprintf("crypto.randomUUID: 读取随机源失败: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10xx (RFC 4122)
	return object.NewString(fmt.Sprintf("%x-%x-%x-%x-%x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
}

// jsGetRandomValues 实现 crypto.getRandomValues(arr) —— 原地填充并返回入参
// (与浏览器一致的返回约定: back with the same array)。
//
// 接受 TypedArray (规范形态) 与数字数组 (Gox 自己的字节数组等价物, 与 hash()
// 的入参口径保持一致)。填充直接写底层字节: 所有视图类型都适用, 无需按元素
// 宽度分派 —— 浮点视图可能因此拿到 NaN/Inf 位型, 浏览器也是这么填的。
func jsGetRandomValues(args ...object.Value) object.Value {
	if len(args) < 1 {
		return object.NewTypeError("crypto.getRandomValues: 缺少目标数组参数")
	}
	switch t := args[0].(type) {
	case *object.TypedArray:
		if t.Buffer == nil || t.Buffer.Detach {
			return object.NewTypeError("crypto.getRandomValues: TypedArray 的 buffer 已分离 (detached)")
		}
		n := t.Length * t.Kind.ElemSize
		if n > cryptoRandomValuesMaxBytes {
			return cryptoQuotaError(n)
		}
		start := t.ByteOffset
		if start < 0 || start+n > len(t.Buffer.Data) {
			return object.NewTypeError("crypto.getRandomValues: TypedArray 视图越界")
		}
		if _, err := rand.Read(t.Buffer.Data[start : start+n]); err != nil {
			return object.NewError(fmt.Sprintf("crypto.getRandomValues: 读取随机源失败: %v", err))
		}
		return t
	case *object.Array:
		n := len(t.Elements)
		if n > cryptoRandomValuesMaxBytes {
			return cryptoQuotaError(n)
		}
		buf := make([]byte, n)
		if _, err := rand.Read(buf); err != nil {
			return object.NewError(fmt.Sprintf("crypto.getRandomValues: 读取随机源失败: %v", err))
		}
		for i, b := range buf {
			t.Elements[i] = object.NewNumber(float64(b))
		}
		return t
	}
	return object.NewTypeError(
		"crypto.getRandomValues: 参数必须是 TypedArray / 字节数组, 收到 %s",
		object.TypeOf(args[0]))
}

// cryptoQuotaError 造一次"要太多随机字节"的 QuotaExceededError。
func cryptoQuotaError(n int) object.Value {
	return object.NewErrorWithName("QuotaExceededError",
		fmt.Sprintf("crypto.getRandomValues: 请求 %d 字节超过 %d 字节上限",
			n, cryptoRandomValuesMaxBytes))
}

// setupCrypto 注册 gx/crypto 模块, 并把同一批实现挂到全局 crypto / hash 上
// (由 SetupGlobals 调用, env 参数保持签名一致)。
func setupCrypto(env *runtime.Environment) {
	object.RegisterBuiltinModule("gx/crypto", func() map[string]object.Value {
		exports := map[string]object.Value{
			"hash":            object.NewBuiltin("hash", jsHash),
			"digest":          object.NewBuiltin("digest", jsDigest),
			"randomUUID":      object.NewBuiltin("randomUUID", jsRandomUUID),
			"getRandomValues": object.NewBuiltin("getRandomValues", jsGetRandomValues),
		}
		// Node 互操作: 默认导出 = 模块命名空间对象。必须在 return 前造,
		// 否则会自引用 (builtinNamespaceObject 遍历的是当前这批导出)。
		exports["default"] = builtinNamespaceObject(exports)
		return exports
	})

	exports, ok := object.LookupBuiltinModule("gx/crypto")
	if !ok {
		// 不可达: RegisterBuiltinModule 刚跑过。显式 panic 好过造出一个空
		// crypto 全局对象 (脚本会在很远的地方报 "subtle is undefined")。
		panic(`stdlib: builtin module "gx/crypto" is not registered`)
	}

	// 浏览器里 crypto 是全局对象 (window.crypto), 不是需要 import 的模块 ——
	// 抄 Web 侧代码 (`crypto.subtle.digest(...)`) 必须开箱可用, 所以全局也建一份。
	subtle := object.NewObject()
	subtle.SetProperty("digest", exports["digest"])
	cryptoObj := object.NewObject()
	cryptoObj.SetProperty("subtle", subtle)
	cryptoObj.SetProperty("randomUUID", exports["randomUUID"])
	cryptoObj.SetProperty("getRandomValues", exports["getRandomValues"])
	// 命名空间对象的 [[Prototype]] 必须是 %Object.prototype% (普通对象)。
	setNamespaceProto(cryptoObj)
	setNamespaceProto(subtle)
	env.Declare("crypto", cryptoObj, false)

	// hash 是 Gox 侧的同步糖, 不在 WebCrypto 里, 故挂在全局而不是 crypto 下
	// —— 放进 crypto.subtle 会让人误以为它是标准的一部分。
	env.Declare("hash", exports["hash"], false)
}
