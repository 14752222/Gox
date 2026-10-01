/*
 * libgox 的 NAPI 胶水 (C 侧)。
 *
 * ── 为什么这些定义不能写在 main.go 的 preamble 里 ────────────────────────
 *
 * cgo 有一条硬规则: **含 `//export` 的文件, 其 preamble 只能有声明, 不能有定义。**
 * 原因是那样文件的 preamble 会被复制进两个不同的 C 输出文件 (普通调用侧 +
 * export 侧), 于是里面任何"有外部链接的定义"都会重复 —— 链接期报:
 *
 *     ld.lld: error: duplicate symbol: gox_trampoline
 *     >>> defined at main.go:123 (.../000000.o:(gox_trampoline))
 *     >>> defined at main.go:123 (.../000001.o:(.text+0x0))
 *
 * 这个报错只在**链接**阶段出现 (类型检查全过), 且"同一行号出现两次"这个特征很
 * 容易被当成工具链 bug。把定义挪到本文件即可 —— 同包目录下的 .c 只编译一次。
 *
 * 另一个相关的坑 (同一个教训的另一面): 本文件里供 Go 取地址的函数**不能是
 * static**。见 gox_trampoline 的注释。
 */

#include <node_api.h>
#include <stdbool.h>
#include <stdlib.h>
#include <string.h>

// ── 分发桥 ────────────────────────────────────────────────────────────────
//
// **为什么要有这一层**: NAPI 的每个导出方法都是同一个签名
// `napi_value (*)(napi_env, napi_callback_info)`, 而 cgo 的 //export 只能导出
// C 可见的普通函数。直接把 napi_env / napi_callback_info 写进 //export 的参数
// 列表会撞上 cgo 对"不完整类型指针"的映射规则 —— 实测报错:
//
//     cannot convert env (variable of type *_Ctype_napi_env) to type _Ctype_napi_env
//     cannot use _cgo1 (…napi_callback_info) as *…struct_napi_callback_info__…
//
// 所以这里在 C 侧做一个 trampoline: 它把参数**摊平成 cgo 认得的形态**
// (env 走 void*、argv 走 void**、长度用 int) 再调 Go。Go 侧只管业务。
//
// 另一个好处: `void* data` 天然携带"是哪个方法" —— 不必给函数对象挂名字属性再
// 反查 (那条路走不通: napi_get_cb_info 不回传函数对象本身)。

// gox_dispatch_go 由 Go 侧 //export 提供。
extern void *gox_dispatch_go(void *env, int method, void **argv, int argc);

// 单个回调最多看 8 个参数 —— 目前最多的一个方法 (init) 用 5 个, 余量足够。
// Go 侧 GoxDispatch 里夹取上限时必须与此一致。
#define GOX_MAX_ARGS 8

// gox_trampoline 必须是**非 static** 的。
//
// 静态函数在 cgo 里"直接调用"没问题 (C 代码与 cgo 生成的 stub 同编译单元), 但只要
// **取它的地址**(这里是把函数指针交给 napi_create_function), cgo 就会生成一个
// `_Cfpvar_fp_gox_trampoline` 变量去引用它, 而 static 函数没有外部符号:
//
//     ld.lld: error: undefined symbol: gox_trampoline
//     >>> referenced by go.go (... main._Cfpvar_fp_gox_trampoline)
//
// 同样是链接期才炸。
napi_value gox_trampoline(napi_env env, napi_callback_info info) {
	size_t argc = GOX_MAX_ARGS;
	napi_value argv[GOX_MAX_ARGS];
	void *data = NULL;
	if (napi_get_cb_info(env, info, &argc, argv, NULL, &data) != napi_ok) {
		napi_value undef = NULL;
		napi_get_undefined(env, &undef);
		return undef;
	}
	void *args[GOX_MAX_ARGS];
	for (size_t i = 0; i < argc && i < GOX_MAX_ARGS; i++) {
		args[i] = (void *)argv[i];
	}
	return (napi_value)gox_dispatch_go((void *)env, (int)(intptr_t)data, args, (int)argc);
}

// ── 参数与返回值助手 (都被 Go 侧调用, 因此都不能是 static) ────────────────

// gox_str 从 napi_value 取一个 UTF-8 字符串 (返回 malloc 的 buffer, 调用方 free)。
// NAPI 没有 JNI 的 GetStringUTFChars 那种"借出"语义, 只能自己分配。
char *gox_str(napi_env env, napi_value v, size_t *out_len) {
	*out_len = 0;
	if (env == NULL || v == NULL) {
		return NULL;
	}
	napi_valuetype t = napi_undefined;
	if (napi_typeof(env, v, &t) != napi_ok || t != napi_string) {
		return NULL;
	}
	size_t n = 0;
	if (napi_get_value_string_utf8(env, v, NULL, 0, &n) != napi_ok) {
		return NULL;
	}
	char *buf = (char *)malloc(n + 1);
	if (buf == NULL) {
		return NULL;
	}
	size_t got = 0;
	if (napi_get_value_string_utf8(env, v, buf, n + 1, &got) != napi_ok) {
		free(buf);
		return NULL;
	}
	buf[got] = '\0';
	*out_len = got;
	return buf;
}

// gox_num / gox_bool 取数字与布尔 (失败时 *ok = false, 由 Go 侧决定缺省值)。
double gox_num(napi_env env, napi_value v, double def, bool *ok) {
	*ok = false;
	if (env == NULL || v == NULL) {
		return def;
	}
	double d = 0;
	if (napi_get_value_double(env, v, &d) != napi_ok) {
		return def;
	}
	*ok = true;
	return d;
}

bool gox_bool(napi_env env, napi_value v, bool def, bool *ok) {
	*ok = false;
	if (env == NULL || v == NULL) {
		return def;
	}
	bool b = false;
	if (napi_get_value_bool(env, v, &b) != napi_ok) {
		return def;
	}
	*ok = true;
	return b;
}

// gox_ret_string / gox_ret_int / gox_ret_bool 把 Go 的结果包成 JS 值。
// 字符串传 NULL 时返回 undefined ("没有值" 与 "空串" 语义不同)。
napi_value gox_ret_string(napi_env env, const char *s) {
	napi_value out = NULL;
	if (s == NULL) {
		napi_get_undefined(env, &out);
		return out;
	}
	napi_create_string_utf8(env, s, NAPI_AUTO_LENGTH, &out);
	return out;
}

napi_value gox_ret_int(napi_env env, int32_t v) {
	napi_value out = NULL;
	napi_create_int32(env, v, &out);
	return out;
}

napi_value gox_ret_bool(napi_env env, bool v) {
	napi_value out = NULL;
	napi_get_boolean(env, v, &out);
	return out;
}

// ── 模块注册 ──────────────────────────────────────────────────────────────
//
// 鸿蒙的 NAPI **没有** "dlopen 后自动找导出符号"这种机制 (与 JNI 由 JVM 按符号名
// 自动绑定完全不同)。ArkTS 里写 `import gox from 'libgox.so'` 时, 运行时会去调用
// **本库自己注册**的那个 napi_module。注册必须在"库被加载"的时刻完成, 所以用
// `__attribute__((constructor))` —— 这是鸿蒙 NDK 的标准写法。
//
// 漏掉这一步的症状非常具体, 且容易归错因:
//
//     ArkTS:  import gox from 'libgox.so'
//     → 报 Cannot find module 'libgox.so' 或拿到 undefined
//
// 而此时 .so 明明躺在 entry/libs/<abi>/ 下, llvm-nm 也能看到 GoxModuleRegister
// 符号。**"文件在、符号在、就是 import 不到" ⇒ 十有八九是没注册 module。**
//
// nm_modname 要与库名对应 (libgox.so → "gox")。名字对不上时同样 import 失败。

// GoxModuleRegister 由 Go 侧 //export 提供。
extern void *GoxModuleRegister(void *env, void *exports);

// 类型适配层: //export 出来的符号是普通的 void* 函数, 而 napi_module 要的是
// napi_value (*)(napi_env, napi_value) 这个具体类型。**不能直接把函数指针强转**
// (那在不同 ABI 上是未定义行为), 所以老老实实包一层。
static napi_value gox_register(napi_env env, napi_value exports) {
	return (napi_value)GoxModuleRegister((void *)env, (void *)exports);
}

static napi_module gox_module = {
	.nm_version = 1,
	.nm_flags = 0,
	.nm_filename = NULL,
	.nm_register_func = gox_register,
	.nm_modname = "gox",
	.nm_priv = NULL,
	.reserved = {0},
};

__attribute__((constructor)) static void gox_module_init(void) {
	napi_module_register(&gox_module);
}
