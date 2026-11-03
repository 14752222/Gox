package vm

import "testing"

// 闭包共享 binding cell 的嵌套兄弟可见性 (rFXXbq batch24)。
//
// 模型: 闭包捕获外层 frame.Locals 前缀切片 (活 cell), callClosure 时把
// CapturedLocals 拷贝成本帧 Locals 快照。兄弟闭包 A 调用兄弟闭包 B、
// B 修改共享外层 let 后, A 继续读 —— A 的快照已过时, 必须读活 cell
// (SharedCells, slot < len(SharedCells) 的外层槽位) 才能看到 B 的修改。
// 此前 OP_LOAD 只读快照, test262 nativeFunctionMatcher.js 的
// eatWhitespace/test/eat 三闭包模式 (validateNativeFunctionSource)
// 在 eatWS 前进 pos 后 test 读到旧值, 全部 built-in 函数 toString 用例被挡。
func TestClosureSiblingNestedWriteVisibility(t *testing.T) {
	// 模式一: 3 个闭包 + while 内 continue + 循环体 const (最小复现)
	got := evalJS(t, `
const mk = (source) => {
  let pos = 0;
  const eatWS = () => {
    while (pos < source.length) {
      const c = source[pos];
      if (c === ' ') { pos += 1; continue; }
      break;
    }
  };
  const test = (s) => { eatWS(); return source.slice(pos, pos + s.length) === s; };
  const eat = (s) => { if (test(s)) { pos += s.length; return true; } return false; };
  return { eat };
};
const m = mk("  x");
m.eat("x") ? "ok" : "stale";
`)
	assertString(t, got, "ok")

	// 模式二: 兄弟闭包嵌套调用修改计数
	got2 := evalJS(t, `
let count = 0;
const inc = () => { count += 1; };
const run = () => { inc(); inc(); return count; };
run() === 2 ? "ok" : "stale";
`)
	assertString(t, got2, "ok")

	// 模式三: for-let 逐轮闭包语义不被破坏 (每轮捕获独立 j)
	got3 := evalJS(t, `
const fns = [];
for (let j = 0; j < 3; j++) { fns.push(() => j); }
fns.map(f => f()).join(",");
`)
	assertString(t, got3, "0,1,2")

	// 模式四: for-var 共享语义同样不破坏 (全部读到终值)
	got4 := evalJS(t, `
const fns = [];
for (var k = 0; k < 3; k++) { fns.push(() => k); }
fns.map(f => f()).join(",");
`)
	assertString(t, got4, "3,3,3")
}
