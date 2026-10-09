package vm

// ===== async generator 的 return(v): 参数在**体内**还是**体外**交割 (rj9MwH) =====
//
// 规范 AsyncGeneratorAwaitReturn / AsyncGeneratorUnwrapYieldResumption:
// return(v) 的 v 都要过一次 PromiseResolve, 但**交割地点**取决于挂起状态 ——
//   - suspendedStart / completed: 体外交割。PromiseResolve 抛错 (v 的
//     constructor getter throw) ⇒ 直接以该值 reject 请求, **体绝不恢复**。
//   - suspendedYield: 体内交割。await 发生在 yield 挂起点, 抛出与被拒都是
//     **回灌进体**的异常 ⇒ 体内的 try/catch 能接住并改写返回值。
//
// 此前 Gox 只有"体外"一条路 (体恢复之后再解包), 于是
// return-suspendedYield-broken-promise-try-catch.js 期望的
// { value: 1, done: true } + caughtErr 变成了对外 reject (3 例全红)。
//
// Node v22 实测对照 (implementations 侧的事实):
//   - suspendedYield + return(Promise.reject(X)) ⇒ {value:1, done:true}, caught=X
//   - suspendedStart + return(Promise.reject(X)) ⇒ reject X, 体不跑

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// agLogField 从用例返回的 log 对象里取一个字段。
func agLogField(t *testing.T, log object.Value, name string) object.Value {
	t.Helper()
	o, ok := log.(*object.Object)
	if !ok {
		t.Fatalf("log 期望 *object.Object, 实得 %T", log)
	}
	v, ok := o.GetProperty(name)
	if !ok {
		t.Fatalf("log 缺少字段 %q", name)
	}
	return v
}

// TestAGReturnBrokenPromiseSuspendedYield 挂起于 yield: broken promise 的
// constructor getter 抛错 ⇒ 抛出值**回灌进体**, 体内 catch 接住后 return 1。
func TestAGReturnBrokenPromiseSuspendedYield(t *testing.T) {
	log := evalJS(t, `
var log = {};
var caught;
var g = async function*() {
  try {
    yield 1;
    return 'this is never returned';
  } catch (err) {
    caught = err;
    return 1;
  }
};
var bp = Promise.resolve(42);
Object.defineProperty(bp, 'constructor', {
  get: function() { throw new Error('broken promise'); }
});
var it = g();
it.next().then(function() {
  return it.return(bp);
}).then(function(ret) {
  log.msg = caught && caught.message;
  log.value = ret.value;
  log.done = ret.done;
});
log;
`)
	assertString(t, agLogField(t, log, "msg"), "broken promise")
	assertNumber(t, agLogField(t, log, "value"), 1)
	assertBoolean(t, agLogField(t, log, "done"), true)
}

// TestAGReturnBrokenPromiseSuspendedStart 尚未启动: 体外交割, 直接 reject,
// **体一次都不能跑**。
func TestAGReturnBrokenPromiseSuspendedStart(t *testing.T) {
	log := evalJS(t, `
var log = {};
var ran = false;
var g = async function*() {
  ran = true;
  yield 1;
};
var bp = Promise.resolve(42);
Object.defineProperty(bp, 'constructor', {
  get: function() { throw new Error('broken promise'); }
});
var it = g();
it.return(bp).then(
  function() { log.way = 'resolved'; },
  function(err) { log.way = 'rejected'; log.msg = err && err.message; }
);
log.ran = ran;
log;
`)
	assertString(t, agLogField(t, log, "way"), "rejected")
	assertString(t, agLogField(t, log, "msg"), "broken promise")
	assertBoolean(t, agLogField(t, log, "ran"), false)
}

// TestAGReturnBrokenPromiseCompleted 已完成: 同样体外交割, 直接 reject。
func TestAGReturnBrokenPromiseCompleted(t *testing.T) {
	log := evalJS(t, `
var log = {};
var g = async function*() {};
var bp = Promise.resolve(42);
Object.defineProperty(bp, 'constructor', {
  get: function() { throw new Error('broken promise'); }
});
var it = g();
it.next().then(function() {
  it.return(bp).then(
    function() { log.way = 'resolved'; },
    function(err) { log.way = 'rejected'; log.msg = err && err.message; }
  );
});
log;
`)
	assertString(t, agLogField(t, log, "way"), "rejected")
	assertString(t, agLogField(t, log, "msg"), "broken promise")
}

// TestAGReturnRejectedPromiseFeedsBody 挂起于 yield + 被拒的 promise: 拒绝原因
// 同样回灌进体 (不只是"getter 抛错"这一条路)。
func TestAGReturnRejectedPromiseFeedsBody(t *testing.T) {
	log := evalJS(t, `
var log = {};
var caught;
var g = async function*() {
  try {
    yield 1;
    return 'never';
  } catch (err) {
    caught = err;
    return 1;
  }
};
var it = g();
it.next().then(function() {
  return it.return(Promise.reject(new Error('X')));
}).then(function(ret) {
  log.msg = caught && caught.message;
  log.value = ret.value;
  log.done = ret.done;
});
log;
`)
	assertString(t, agLogField(t, log, "msg"), "X")
	assertNumber(t, agLogField(t, log, "value"), 1)
	assertBoolean(t, agLogField(t, log, "done"), true)
}

// TestAGReturnValueSurvivesFinallyYield 挂起于 yield 且体带 finally:
// 体内 await 不得把挂起的 return 值弄丢 —— finally 里的 yield 之后, 下一跳
// 必须产出当初传进去的 'sent-value'。
//
// 这条同时钉住一条实现纪律: 恢复生成器**不能**在 promise 回调帧里做。Gox
// 的 then 回调是同步跑的, 在回调里驱动会让 VM 按回调栈深换算挂起状态,
// PendingVal 随之丢失 (第三跳退化成 undefined)。
func TestAGReturnValueSurvivesFinallyYield(t *testing.T) {
	log := evalJS(t, `
var log = {};
var g = async function*() {
  try {
    yield 1;
  } finally {
    yield 2;
  }
};
var it = g();
it.next().then(function(ret) {
  log.a = ret.value;
  return it.return('sent-value');
}).then(function(ret) {
  log.b = ret.value;
  log.bDone = ret.done;
  return it.next();
}).then(function(ret) {
  log.c = ret.value;
  log.cDone = ret.done;
  return it.next();
}).then(function(ret) {
  log.d = ret.value;
  log.dDone = ret.done;
});
log;
`)
	assertNumber(t, agLogField(t, log, "a"), 1)
	assertNumber(t, agLogField(t, log, "b"), 2)
	assertBoolean(t, agLogField(t, log, "bDone"), false)
	assertString(t, agLogField(t, log, "c"), "sent-value")
	assertBoolean(t, agLogField(t, log, "cDone"), true)
	assertBoolean(t, agLogField(t, log, "dDone"), true)
}

// TestAGReturnPromiseUnwrappedWithFinally 正常兑现路径: 参数解包后传给体,
// finally 照常展开 (对照组 —— 证明"体内交割"没有改掉兑现时的语义)。
func TestAGReturnPromiseUnwrappedWithFinally(t *testing.T) {
	o, ok := evalJS(t, `
var log = [];
var g = async function*() {
  try {
    yield 1;
  } finally {
    log.push('finally');
  }
};
var it = g();
it.next().then(function() {
  return it.return(Promise.resolve(7));
}).then(function(ret) {
  log.push('value=' + ret.value);
  log.push('done=' + ret.done);
});
log;
`).(*object.Array)
	if !ok {
		t.Fatal("期望 log 是数组")
	}
	want := []string{"finally", "value=7", "done=true"}
	if len(o.Elements) != len(want) {
		t.Fatalf("log 长度期望 %d, 实得 %d", len(want), len(o.Elements))
	}
	for i, w := range want {
		if s, isStr := o.Elements[i].(*object.String); !isStr || s.Value != w {
			t.Fatalf("log[%d] 期望 %q, 实得 %v", i, w, o.Elements[i])
		}
	}
}
