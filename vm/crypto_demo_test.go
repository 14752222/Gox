package vm

// ===== gx/crypto 的 JS 可见面 (看板 ru3TZK) =====
//
// stdlib/crypto_test.go 钉的是 Go 侧契约 (导出表、指纹、错误分支)。本文件钉
// 的是**JS 层真的拿得到**: 全局声明漏一步, Go 侧单测全绿而脚本里 typeof
// 仍是 undefined —— 这正是此前"stdlib/ 全域无 crypto"的失败形态。
//
// 接线纪律 (与 vm/http_demo_test.go 同): digest 的结算发生在 0ms 定时器上,
// 所以必须走 EvalVM/EvalFileVM + RunTimers; 只用 evalWithStdlib (vm.Run)
// 的话事件循环根本没被驱动, Promise 永远停在 pending。

import (
	"path/filepath"
	"regexp"
	"testing"

	"github.com/14752222/Gox/object"
)

// cryptoSHA256OfGox 是 sha256("gox") 的指纹, 由 `printf gox | sha256sum` 核对过。
const cryptoSHA256OfGox = "95b1f0ce16eec5ba288547239e93edb1677e5c70166be4716e34a64d82cab507"

// TestCryptoGlobalsVisibleFromJS: 全局 crypto / hash 必须可达且类型正确。
func TestCryptoGlobalsVisibleFromJS(t *testing.T) {
	cases := []struct{ src, want string }{
		{`typeof crypto;`, "object"},
		{`typeof crypto.subtle;`, "object"},
		{`typeof crypto.subtle.digest;`, "function"},
		{`typeof crypto.randomUUID;`, "function"},
		{`typeof crypto.getRandomValues;`, "function"},
		{`typeof hash;`, "function"},
	}
	for _, tc := range cases {
		if got := evalWithStdlib(t, tc.src).Inspect(); got != tc.want {
			t.Errorf("%s 应为 %s, 得到 %s", tc.src, tc.want, got)
		}
	}
}

// TestCryptoHashFromJS: 同步 hash 在 JS 层的值与入参形态。
func TestCryptoHashFromJS(t *testing.T) {
	if got := evalWithStdlib(t, `hash("gox");`).Inspect(); got != cryptoSHA256OfGox {
		t.Errorf(`hash("gox") = %s, 期望 %s`, got, cryptoSHA256OfGox)
	}
	// 算法名大小写不敏感 (WebCrypto 口径)
	if got := evalWithStdlib(t, `hash("gox", "sha256");`).Inspect(); got != cryptoSHA256OfGox {
		t.Errorf(`hash("gox","sha256") = %s, 期望与 SHA-256 相同`, got)
	}
	// Uint8Array / 字节数组 与 string 必须算出同一个指纹 —— 否则 apps #6
	// (重复文件查找) 会把同一份文件的两种读法判成两个文件。
	for _, src := range []string{
		`hash(new Uint8Array([103, 111, 120]));`,
		`hash(new Uint8Array([103, 111, 120]).buffer);`,
		`hash([103, 111, 120]);`,
	} {
		if got := evalWithStdlib(t, src).Inspect(); got != cryptoSHA256OfGox {
			t.Errorf("%s = %s, 期望 %s", src, got, cryptoSHA256OfGox)
		}
	}
	// 未知算法必须抛 (try/catch 接得到), 不能静默出摘要
	errSrc := `
		let caught = "none";
		try { hash("gox", "SHA-999"); } catch (e) { caught = e.name; }
		caught;`
	if got := evalWithStdlib(t, errSrc).Inspect(); got != "TypeError" {
		t.Errorf("未知算法应抛 TypeError, 得到 %s", got)
	}
}

// TestCryptoDigestPromiseFromJS: digest 的 Promise 经事件循环结算成 ArrayBuffer。
func TestCryptoDigestPromiseFromJS(t *testing.T) {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	v, err := EvalVM(`
		async function run() {
			const ab = await crypto.subtle.digest("SHA-256", "gox");
			globalThis.__hex = "";
			const view = new Uint8Array(ab);
			for (let i = 0; i < view.length; i++) {
				__hex += view[i].toString(16).padStart(2, "0");
			}
			globalThis.__len = ab.byteLength;
		}
		run();`)
	if err != nil {
		t.Fatalf("执行脚本: %v", err)
	}
	if err := v.RunTimers(); err != nil {
		t.Fatalf("事件循环: %v", err)
	}
	hex, ok := v.Globals().Get("__hex")
	if !ok {
		t.Fatal("digest 的 await 没有恢复 —— Promise 一直 pending")
	}
	if got := hex.Inspect(); got != cryptoSHA256OfGox {
		t.Errorf("crypto.subtle.digest 结果 = %s, 期望 %s", got, cryptoSHA256OfGox)
	}
	if len, _ := v.Globals().Get("__len"); len.Inspect() != "32" {
		t.Errorf("ArrayBuffer 长度应为 32, 得到 %s", len.Inspect())
	}
}

// TestCryptoRandomUUIDFromJS: randomUUID 在 JS 层给出 v4 UUID。
func TestCryptoRandomUUIDFromJS(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	for i := 0; i < 20; i++ {
		got := evalWithStdlib(t, `crypto.randomUUID();`).Inspect()
		if !re.MatchString(got) {
			t.Fatalf("crypto.randomUUID() = %q, 不是 v4 UUID", got)
		}
	}
	// 两次不得相同 (随机源退化成常量时这里会先红)
	src := `let a = crypto.randomUUID(); let b = crypto.randomUUID(); "" + (a !== b);`
	if got := evalWithStdlib(t, src).Inspect(); got != "true" {
		t.Error("两次 randomUUID() 结果相同 —— 随机源退化")
	}
}

// TestCryptoDemoScript: 跑仓库里那份给人看的演示脚本本身。
//
// 演示脚本不参与编译, 悄悄过期没人知道 (与 http_demo 同一条纪律)。
func TestCryptoDemoScript(t *testing.T) {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	v, err := EvalFileVM(filepath.Join("..", "testdata", "crypto_demo.js"))
	if err != nil {
		t.Fatalf("执行脚本: %v", err)
	}
	if err := v.RunTimers(); err != nil {
		t.Fatalf("事件循环: %v", err)
	}
	summary, ok := v.Globals().Get("__cryptoDemoSummary")
	if !ok {
		t.Fatal("脚本没有导出 __cryptoDemoSummary")
	}
	obj, isObj := summary.(*object.Object)
	if !isObj {
		t.Fatalf("__cryptoDemoSummary 应该是对象, 得到 %s", summary.Inspect())
	}
	checks := map[string]string{
		"sha256":      cryptoSHA256OfGox,
		"digestHex":   cryptoSHA256OfGox,
		"digestBytes": "32",
		"sameImpl":    "true", // 全局 hash 与模块导出必须是同一个对象
	}
	for key, want := range checks {
		got, found := obj.GetProperty(key)
		if !found {
			t.Errorf("__cryptoDemoSummary 缺少 %q", key)
			continue
		}
		if got.Inspect() != want {
			t.Errorf("__cryptoDemoSummary.%s = %s, 期望 %s", key, got.Inspect(), want)
		}
	}
}
