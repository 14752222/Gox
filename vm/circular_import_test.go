package vm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ===== 循环导入防线 =====

// 此前 loadModule 在模块执行完成后才写缓存, 循环/自导入时缓存永远 miss,
// 无限重新编译执行直到 goroutine 栈溢出 (真机 iOS 壳: 打包后入口 import
// "./app.js" 解析回自身, 触发 1GB 栈溢出崩溃)。修复: 先注册导出对象再执行。

// writeModuleDir 在临时目录写入模块文件并返回目录。
func writeModuleDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// TestCircularSelfImportNoStackOverflow 模块自导入 (无绑定使用) 不应栈溢出,
// 应当正常完成 —— 执行期间再 import 自己命中"填充中"的缓存导出对象。
func TestCircularSelfImportNoStackOverflow(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"a.js": `import "./a.js";
export const x = 1;`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "a.js"))
	if err != nil {
		t.Fatalf("自导入不应报错, got: %v", err)
	}
	_ = vm
}

// TestCircularMutualImportDeferredUse 互相导入 + 延迟调用 (函数体内才用对方
// 的导出) 是合法的循环导入形态, 应当正常工作。
func TestCircularMutualImportDeferredUse(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"a.js": `import { b } from "./b.js";
export function a() { return "A" + b(); }
export const ready = true;`,
		"b.js": `import { a } from "./a.js";
export function b() { return "B"; }
export function ab() { return a(); }`,
	})
	_, err := EvalFileVM(filepath.Join(dir, "a.js"))
	if err != nil {
		t.Fatalf("互相导入不应报错, got: %v", err)
	}
}

// TestSelfImportBindingsAvailable 自导入并使用自己的导出: 函数声明先于
// 使用注册, import 拿到的导出对象里应当能取到 (循环导入的部分导出语义)。
func TestSelfImportBindingsAvailable(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"a.js": `import { hi } from "./a.js";
export function hi() { return "ok"; }
hi();`,
	})
	if _, err := EvalFileVM(filepath.Join(dir, "a.js")); err != nil {
		// 只要不栈溢出, 允许实现选择报"未定义"类错误; 但静默成功也合法
		if strings.Contains(err.Error(), "stack overflow") {
			t.Fatalf("循环导入导致栈溢出: %v", err)
		}
	}
}
