package stdlib

import (
	"crypto/sha256"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

// ===== gx/crypto 的契约 (看板 ru3TZK) =====
//
// 为什么这些用例盯的是**已知向量**而不是"两次调用结果相同": 摘要错了不会
// 报错, 只会让比对永远不等 (apps #6 重复文件查找会把所有文件判成互不相同,
// 且不吭声)。所以每条算法都钉一个外部可核对的常量指纹。

// cryptoSetupOnce / cryptoSetupEnv: 整个文件共用**一次** SetupGlobals 的产物。
//
// 为什么必须显式触发: gx/* 系列 (storage / update / crypto) 的
// RegisterBuiltinModule 写在 setup* 里、由 SetupGlobals 调用, 不像 fs/http
// 那样走 init() —— 不跑 SetupGlobals 的话 LookupBuiltinModule 是空的
// (`gox types` 也踩过同一个坑, 见 cmd/gox/cmd_types.go 的 registerOnce)。
//
// 为什么只跑**一次**: RegisterBuiltinModule 会清掉模块导出表缓存, 于是每跑
// 一次 SetupGlobals 就换一批函数对象。断言"全局 crypto.digest 与模块导出是
// 同一个对象"时, 两次 SetupGlobals 的产物天然不相等 —— 那测的是注册机制,
// 不是要守的契约。
var (
	cryptoSetupOnce sync.Once
	cryptoSetupEnv  *runtime.Environment
)

func cryptoSetup(t *testing.T) *runtime.Environment {
	t.Helper()
	cryptoSetupOnce.Do(func() { cryptoSetupEnv = SetupGlobals() })
	return cryptoSetupEnv
}

// cryptoExports 取内置模块 gx/crypto 的导出表 (未注册即失败 —— 漏注册时
// `import { hash } from "gx/crypto"` 是编译期报错, 这里提前一步兜住)。
func cryptoExports(t *testing.T) map[string]object.Value {
	t.Helper()
	cryptoSetup(t)
	exports, ok := object.LookupBuiltinModule("gx/crypto")
	if !ok {
		t.Fatal(`内置模块 "gx/crypto" 未注册`)
	}
	return exports
}

// cryptoCall 调用模块导出表里的一个函数。
//
// 直接取 *object.BuiltinFunction 的 Go 函数体, 不走 object.CallFunction:
// 后者要经 vm 注册的回调桥 (stdlib 不能反向 import vm, 依赖方向是
// vm → stdlib), 未注册时它一律返回 undefined —— 那会让所有断言在
// "看起来跑过、其实没执行"的状态下通过。
func cryptoCall(t *testing.T, name string, args ...object.Value) object.Value {
	t.Helper()
	exports := cryptoExports(t)
	fn, isBuiltin := exports[name].(*object.BuiltinFunction)
	if !isBuiltin {
		t.Fatalf("gx/crypto 缺少导出 %q (或它不是内建函数)", name)
	}
	return fn.Fn(args...)
}

// cryptoHex 跑 hash(data, algo?) 并取回 hex 字符串。
func cryptoHex(t *testing.T, args ...object.Value) string {
	t.Helper()
	got := cryptoCall(t, "hash", args...)
	s, isStr := got.(*object.String)
	if !isStr {
		t.Fatalf("hash(...) 应返回字符串, 得到 %s (%T)", got.Inspect(), got)
	}
	return s.Value
}

// TestCryptoModuleRegistered: 模块与 global 都必须可用, 且共用同一批实现。
func TestCryptoModuleRegistered(t *testing.T) {
	exports := cryptoExports(t)
	for _, name := range []string{"hash", "digest", "randomUUID", "getRandomValues"} {
		if _, ok := exports[name]; !ok {
			t.Errorf("gx/crypto 缺少导出 %q", name)
		}
	}
	// default = 模块命名空间对象, 支撑 `import crypto from "gx/crypto"`。
	if _, ok := exports["default"]; !ok {
		t.Errorf(`gx/crypto 缺少 default —— ` + "`import x from \"gx/crypto\"`" + ` 会拿到 undefined`)
	}

	env := cryptoSetup(t)
	global, ok := env.Get("crypto")
	if !ok {
		t.Fatal("全局 crypto 未声明")
	}
	cryptoObj, isObj := global.(*object.Object)
	if !isObj {
		t.Fatalf("全局 crypto 不是对象: %T", global)
	}
	// 全局与模块必须是**同一个函数对象**: 两份实现迟早漂移, 且不会报错。
	for _, name := range []string{"randomUUID", "getRandomValues"} {
		got, found := cryptoObj.GetProperty(name)
		if !found {
			t.Errorf("全局 crypto 缺少 %q", name)
			continue
		}
		if got != exports[name] {
			t.Errorf("全局 crypto.%s 与模块导出不是同一对象 (实现被复制了)", name)
		}
	}
	subtleVal, found := cryptoObj.GetProperty("subtle")
	if !found {
		t.Fatal("全局 crypto 缺少 subtle")
	}
	subtle, isObj := subtleVal.(*object.Object)
	if !isObj {
		t.Fatalf("crypto.subtle 不是对象: %T", subtleVal)
	}
	if got, _ := subtle.GetProperty("digest"); got != exports["digest"] {
		t.Error("crypto.subtle.digest 与模块导出不是同一对象")
	}
	if got, ok := env.Get("hash"); !ok || got != exports["hash"] {
		t.Error("全局 hash 与模块导出不是同一对象")
	}
}

// TestCryptoHashKnownVectors: 各算法的指纹必须与公开向量一致。
func TestCryptoHashKnownVectors(t *testing.T) {
	cases := []struct {
		algo string
		in   string
		want string
	}{
		{"MD5", "abc", "900150983cd24fb0d6963f7d28e17f72"},
		{"SHA-1", "abc", "a9993e364706816aba3e25717850c26c9cd0d89d"},
		{"SHA-256", "abc", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{"SHA-512", "abc", "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a" +
			"2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f"},
		// 空输入: 退化实现最常见的错法是把 nil 当"没数据"跳过 Write。
		{"SHA-256", "", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
	}
	for _, tc := range cases {
		if got := cryptoHex(t, object.NewString(tc.in), object.NewString(tc.algo)); got != tc.want {
			t.Errorf("hash(%q, %q) = %s, 期望 %s", tc.in, tc.algo, got, tc.want)
		}
	}
	// 缺省算法 = SHA-256: 不给算法名也能用, 且不是悄悄换算法。
	if got := cryptoHex(t, object.NewString("abc")); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Errorf("hash(%q) 缺省算法应等价于 SHA-256, 得到 %s", "abc", got)
	}
}

// TestCryptoHashAlgoNameCaseInsensitive: 算法名按 WebCrypto 口径大小写/分隔符不敏感。
func TestCryptoHashAlgoNameCaseInsensitive(t *testing.T) {
	want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	for _, spelling := range []string{"SHA-256", "sha256", "Sha_256", "SHA256", "sha-256"} {
		if got := cryptoHex(t, object.NewString("abc"), object.NewString(spelling)); got != want {
			t.Errorf("hash(..., %q) = %s, 期望与 SHA-256 相同 (%s)", spelling, got, want)
		}
	}
}

// TestCryptoHashBinaryInputs: 四种入参形态必须算出**同一份**字节的摘要。
//
// 这条同时钉住 TypedArray 的 byteOffset 处理: 视图不是从 buffer 开头起的
// (subarray 的常见结果), 直接拿整块 buffer 会算出另一个指纹, 且不报错。
func TestCryptoHashBinaryInputs(t *testing.T) {
	raw := []byte("gox-ru3TZK")
	want := sha256.Sum256(raw)
	wantHex := hexOf(want[:])

	// (1) string —— 按 UTF-8 编码 (与 TextEncoder 同口径)
	if got := cryptoHex(t, object.NewString(string(raw))); got != wantHex {
		t.Errorf("hash(string) = %s, 期望 %s", got, wantHex)
	}
	// (2) ArrayBuffer
	ab := object.NewArrayBuffer(len(raw))
	copy(ab.Data, raw)
	if got := cryptoHex(t, ab, object.NewString("SHA-256")); got != wantHex {
		t.Errorf("hash(ArrayBuffer) = %s, 期望 %s", got, wantHex)
	}
	// (3) TypedArray (Uint8Array)
	ta := object.NewTypedArray(object.MustLookupTAKind("Uint8Array"), len(raw))
	copy(ta.Buffer.Data, raw)
	if got := cryptoHex(t, ta, object.NewString("SHA-256")); got != wantHex {
		t.Errorf("hash(Uint8Array) = %s, 期望 %s", got, wantHex)
	}
	// (4) TypedArray **带 byteOffset**: 视图覆盖 buffer 中段
	buf := object.NewArrayBuffer(len(raw) + 3)
	copy(buf.Data[3:], raw)
	view := object.NewTypedArrayView(object.MustLookupTAKind("Uint8Array"), buf, 3, len(raw))
	if got := cryptoHex(t, view, object.NewString("SHA-256")); got != wantHex {
		t.Errorf("hash(Uint8Array with byteOffset) = %s, 期望 %s", got, wantHex)
	}
	// (5) 数字数组 —— fs.readBytesSync 的返回形 (apps #6 重复文件查找走这条)
	arr := object.NewArray(nil)
	for _, b := range raw {
		arr.Elements = append(arr.Elements, object.NewNumber(float64(b)))
	}
	if got := cryptoHex(t, arr, object.NewString("SHA-256")); got != wantHex {
		t.Errorf("hash(字节数组) = %s, 期望 %s", got, wantHex)
	}
}

// TestCryptoHashRejectsBadArguments: 错误必须**报出来**, 不能静默出摘要。
func TestCryptoHashRejectsBadArguments(t *testing.T) {
	// 未知算法: 静默回退到默认算法 = 指纹对不上但不报错, 最坏的一类。
	got := cryptoCall(t, "hash", object.NewString("abc"), object.NewString("SHA-999"))
	err, isErr := got.(*object.Error)
	if !isErr {
		t.Fatalf("未知算法应返回错误, 得到 %s", got.Inspect())
	}
	if err.Name != "TypeError" {
		t.Errorf("未知算法的错误名应为 TypeError, 得到 %s", err.Name)
	}
	// 报错里要列出可用算法, 否则调用方只能去翻源码。
	if !strings.Contains(err.Message, "SHA-256") {
		t.Errorf("错误信息应列出受支持的算法, 得到 %q", err.Message)
	}

	// 不支持的入参类型
	if got := cryptoCall(t, "hash", object.NewNumber(42)); !isErrorValue(got) {
		t.Errorf("hash(42) 应报错, 得到 %s", got.Inspect())
	}
	// 缺参数
	if got := cryptoCall(t, "hash"); !isErrorValue(got) {
		t.Errorf("hash() 应报错, 得到 %s", got.Inspect())
	}
}

// cryptoDrainDigestTimers 只跑 digest 自己注册的 0ms 定时器。
//
// 为什么**按名字过滤**而不是把到期定时器全跑一遍: 调度器是进程级单例,
// 同包其它用例注册的定时器不该被本文件顺手触发 (那会让用例之间互相污染,
// 症状随机)。
func cryptoDrainDigestTimers(t *testing.T) {
	t.Helper()
	sched := object.GlobalScheduler()
	for _, tm := range sched.DueTimers() {
		fn, isBuiltin := tm.Callback.(*object.BuiltinFunction)
		if !isBuiltin || fn.Name != "__crypto_digest_task" {
			sched.Reschedule(tm)
			continue
		}
		fn.Fn()
		sched.Reschedule(tm)
	}
}

// TestCryptoDigestResolvesArrayBuffer: digest 返回 Promise, resolve 成 ArrayBuffer。
func TestCryptoDigestResolvesArrayBuffer(t *testing.T) {
	got := cryptoCall(t, "digest", object.NewString("SHA-256"), object.NewString("abc"))
	p, isPromise := got.(*object.Promise)
	if !isPromise {
		t.Fatalf("digest 应返回 Promise, 得到 %s (%T)", got.Inspect(), got)
	}
	// 计算被推到下一个 tick, 所以此刻必须是 pending —— 不是"假装异步"。
	if p.State != object.PromisePending {
		t.Fatalf("digest 返回的 Promise 在派发前应为 pending, 得到 state=%d", p.State)
	}

	cryptoDrainDigestTimers(t)
	if p.State != object.PromiseFulfilled {
		t.Fatalf("派发后应为 fulfilled, 得到 state=%d (reason=%s)", p.State, p.Reason.Inspect())
	}
	ab, isAB := p.Value.(*object.ArrayBuffer)
	if !isAB {
		t.Fatalf("digest 应 resolve 成 ArrayBuffer, 得到 %s (%T)", p.Value.Inspect(), p.Value)
	}
	want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got := hexOf(ab.Data); got != want {
		t.Errorf("digest 结果 = %s, 期望 %s", got, want)
	}
	if len(ab.Data) != 32 {
		t.Errorf("SHA-256 的 ArrayBuffer 应为 32 字节, 得到 %d", len(ab.Data))
	}
}

// TestCryptoDigestRejectsUnsupportedAlgo: 错误走 reject (规范口径), 且
// MD5 不进 subtle —— 浏览器里没有这个算法, 抄过来的代码不该在这里通过。
func TestCryptoDigestRejectsUnsupportedAlgo(t *testing.T) {
	for _, algo := range []string{"MD5", "SHA-999"} {
		got := cryptoCall(t, "digest", object.NewString(algo), object.NewString("abc"))
		p, isPromise := got.(*object.Promise)
		if !isPromise {
			t.Fatalf("digest(%q) 应返回 Promise, 得到 %s", algo, got.Inspect())
		}
		if p.State != object.PromiseRejected {
			t.Fatalf("digest(%q) 应 reject, 得到 state=%d", algo, p.State)
		}
	}
	// 参数不齐同样 reject 而不是同步抛: await 一侧只写 try/catch 就够。
	got := cryptoCall(t, "digest", object.NewString("SHA-256"))
	if p, isPromise := got.(*object.Promise); !isPromise || p.State != object.PromiseRejected {
		t.Fatalf("digest 参数不齐应返回 rejected Promise, 得到 %s", got.Inspect())
	}
}

// TestCryptoRandomUUID: 形状、版本位与不可预测性 (两次不得相同)。
func TestCryptoRandomUUID(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		got := cryptoCall(t, "randomUUID")
		s, isStr := got.(*object.String)
		if !isStr {
			t.Fatalf("randomUUID() 应返回字符串, 得到 %s (%T)", got.Inspect(), got)
		}
		if !re.MatchString(s.Value) {
			t.Fatalf("randomUUID() = %q, 不是 v4 UUID", s.Value)
		}
		if seen[s.Value] {
			t.Fatalf("randomUUID() 重复: %q —— 随机源退化成了可预测值", s.Value)
		}
		seen[s.Value] = true
	}
}

// TestCryptoGetRandomValues: 原地填充、返回入参、超上限报错。
func TestCryptoGetRandomValues(t *testing.T) {
	ta := object.NewTypedArray(object.MustLookupTAKind("Uint8Array"), 32)
	got := cryptoCall(t, "getRandomValues", ta)
	if got != object.Value(ta) {
		t.Errorf("getRandomValues 应返回入参本身, 得到 %s", got.Inspect())
	}
	// 32 字节全零的概率是 2^-256, 全零即"根本没填"。
	if allZero(ta.Buffer.Data[:32]) {
		t.Error("getRandomValues 填出来的 32 字节全是 0 —— 随机源没生效")
	}
	// 两次不得相同
	other := object.NewTypedArray(object.MustLookupTAKind("Uint8Array"), 32)
	cryptoCall(t, "getRandomValues", other)
	if string(other.Buffer.Data[:32]) == string(ta.Buffer.Data[:32]) {
		t.Error("两次 getRandomValues 结果相同 —— 随机源退化")
	}

	// 数字数组 (Gox 的字节数组等价物)
	arr := object.NewArray([]object.Value{
		object.NewNumber(0), object.NewNumber(0), object.NewNumber(0), object.NewNumber(0),
	})
	cryptoCall(t, "getRandomValues", arr)
	if len(arr.Elements) != 4 {
		t.Fatalf("getRandomValues 不应改变数组长度, 得到 %d", len(arr.Elements))
	}
	for i, el := range arr.Elements {
		n, isNum := el.(*object.Number)
		if !isNum || n.Value < 0 || n.Value > 255 {
			t.Errorf("arr[%d] = %s, 期望 0-255 的字节", i, el.Inspect())
		}
	}

	// 超上限 (WebCrypto 的 64KB) 必须报错, 不能静默截断
	big := object.NewTypedArray(object.MustLookupTAKind("Uint8Array"), cryptoRandomValuesMaxBytes+1)
	errVal := cryptoCall(t, "getRandomValues", big)
	err, isErr := errVal.(*object.Error)
	if !isErr {
		t.Fatalf("超上限应报错, 得到 %s", errVal.Inspect())
	}
	if err.Name != "QuotaExceededError" {
		t.Errorf("错误名应为 QuotaExceededError, 得到 %s", err.Name)
	}
	// 非数组入参
	if got := cryptoCall(t, "getRandomValues", object.NewString("x")); !isErrorValue(got) {
		t.Errorf("getRandomValues(字符串) 应报错, 得到 %s", got.Inspect())
	}
}

// ===== 小工具 =====

// hexOf 把字节渲染成小写 hex (测试断言用, 与 hash() 的返回口径一致)。
func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&0x0f])
	}
	return string(out)
}

// isErrorValue 判断值是否为 *object.Error (VM 会把它当 JS 异常抛出)。
func isErrorValue(v object.Value) bool {
	_, ok := v.(*object.Error)
	return ok
}

// allZero 判断字节切片是否全零。
func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}
