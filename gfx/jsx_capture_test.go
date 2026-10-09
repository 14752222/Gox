package gfx

// JSX 回调捕获外层绑定的端到端回归 (rtWB7N)。
//
// 这条是 bug 报告的**原始形态**: 跨模块工厂返回 + 解构 + JSX, 且 JSX 里的
// onDraw 回调引用了工厂局部的 signal 与跨模块 import 绑定。它曾经抛
// `ReferenceError: Cannot access lexical declaration before initialization`。
//
// vm/capture_prefix_test.go 用纯 JS 覆盖了同一根因的最小形态; 这条补的是
// "真 JSX + 真 canvas + 真渲染" 这一层 —— 它确保编译器的槽位修复在 JSX
// 降级 (parser 把标签转成 h(...) 调用) 之后依然成立。

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// writeJSXCaptureProject 把三文件模块工程写进临时目录, 返回入口路径。
//
// 必须走 EvalFileVM (而不是 EvalVM): 只有前者会 SetModuleBase, 相对 import
// 才解析得到 —— 与真实 `gox <文件>` 是同一条代码路径。
func writeJSXCaptureProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"dep.js": `export function bump() { return 1; }`,
		// 工厂里的绑定有两样: 解构出来的 signal 读数 n, 以及跨模块 import 的 bump。
		// onDraw 闭包两个都引用, 而中间层 W 自己一个都不引用 —— 正是出事的形状。
		"mod.js": `
import { bump } from "./dep.js";
import { h } from "gx/gfx";
import { createSignal } from "gx/solid";
export function makeWidget() {
  const [n, setN] = createSignal(2);
  const W = (p) => (
    <canvas width={80} height={40} onDraw={(ctx) => {
      globalThis.log.push("draw:" + (bump() + n()));
    }} />
  );
  return { W, setN };
}`,
		"entry.js": `
import { makeWidget } from "./mod.js";
import { h, render } from "gx/gfx";
globalThis.log = [];
const { W, setN } = makeWidget();
render(<window title="tdz" width={200} height={120}><W /></window>);`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("写 %s: %v", name, err)
		}
	}
	return filepath.Join(dir, "entry.js")
}

// TestJSXFactoryCallbackCapturesLocal 组合形态不得抛 TDZ, 且回调里读到的值
// 必须正确 (bump()=1 + n()=2 ⇒ "draw:3")。
func TestJSXFactoryCallbackCapturesLocal(t *testing.T) {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalFileVM(writeJSXCaptureProject(t))
	if err != nil {
		t.Fatalf("不该抛 TDZ: %v", err)
	}

	// 泵两轮再收工: canvas 的 onDraw 在挂载时就被调来做依赖收集, 但回调必须
	// 在事件循环里跑才生效 (主脚本跑完后 currentVM 为 nil, 回调桥是空操作)。
	round := 0
	err = v.RunTimersWithPump(func(maxWait time.Duration) bool {
		const pollWait = 10 * time.Millisecond
		if maxWait <= 0 || maxWait > pollWait {
			maxWait = pollWait
		}
		round++
		if round > 2 {
			fake.push(Event{Kind: EventClose})
			return Pump(maxWait)
		}
		return Pump(maxWait)
	})
	if err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}

	got := jsArrayStr(t, v, "log")
	if len(got) == 0 {
		t.Fatal("onDraw 一次都没跑 (或跑的时候抛错了)")
	}
	want := "draw:3"
	for _, s := range got {
		if s != want {
			t.Fatalf("onDraw 读到 %q, want %q (全量 %v) —— 多半是外层绑定读成了 TDZ 槽/快照", s, want, got)
		}
	}
}
