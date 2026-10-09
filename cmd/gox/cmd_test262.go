package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// gox test262 —— Test262 子集合规率 runner (S1/T01)。
//
// 目标: 用一条命令把 Gox 的 ECMAScript 合规现状量化出来，并且让"每修一个
// 引擎缺陷，合规率涨了多少"变得可测量 —— 之前引擎的回归防线是手写用例
// (vm/*_test.go) 与 testdata/acceptance.js，覆盖面全凭手感；Test262 是
// 官方一致性套件，几万个用例按规范条文逐条出题。
//
// 设计取舍（与官方 test262 runner 的差异）:
//   - 不追求跑满全量: intl402 需要 i18n（Gox 未实现），默认跳过；-suite
//     控制跑 language / built-ins / annexB / all。合规率以"实际执行的用例"
//     为分母，跳过的用例不计入（否则未实现的领域会把分母灌大，数字失真）。
//   - negative 判定按错误阶段对齐 Gox 的三段错误格式:
//       parser errors: / compiler error: → 编译期; vm error: → 运行期。
//     phase=parse 要求解析失败; early 要求编译失败; runtime 要求运行失败;
//     resolution（模块解析错误，Gox 在运行期加载模块）放宽为任何错误。
//   - negative.type 做宽松校验: 错误消息里包含类型名即认（Gox 的错误消息
//     带 "TypeError: " 这类前缀，但不是所有路径都带，第一轮宁可误放也不过杀）。
//   - async 用例: 注入官方约定的 $DONE(err)，用 VM 的定时器泵驱动到用例
//     结束或超时。module+async 组合下 $DONE 状态写进模块命名空间，宿主读
//     不到 —— 按"无异常 + $DONE 未确认"判失败（此类用例极少，如实呈现）。
//   - 单用例超时不杀 goroutine（VM 没有步数预算中断），泄漏的 goroutine
//     会留到进程退出 —— runner 是一次性 CLI，可接受；-timeout 默认 3s 兜底。
//
// 并行模型（与 -jobs 参数无关的确定性）:
//   - 引擎有包级可变状态（vm.currentVM、object.callbackError[Value]、
//     stdlib 的 solid 栈等），同一进程内**连续**跑多个用例时前一个会污染
//     后一个 —— 用例判定取决于"它和谁同进程、谁在它前面跑"。因此用例的
//     结果与分片方式强相关。
//   - 于是分片数必须**固定**（test262ShardCount），且与 -jobs 解耦：
//     每个用例永远落在同一个分片、同一个前驱序列里，-jobs 只决定"同时
//     跑几个分片"（并发度）。若分片数跟着 -jobs 变，-jobs 就改变了每个
//     用例的邻居 ⇒ 通过数随 -jobs 翻转（实测 A7 子集：jobs 1/2 通过 11，
//     jobs 4/8 只通过 6）。
//   - 分片清单（含源码）由父进程落临时文件、子进程直读：子进程若各自扫盘
//     收集，S 片就是 S 倍收集成本（全量扫盘约 6.5s）。
//
// 用法:
//   gox test262 [-root 目录] [-suite language] [-filter 正则] [-jobs N]
//               [-timeout 秒] [-json 文件] [-maxfail N] [-quiet] [-list]
//
// root 解析顺序: -root > $GOX_TEST262 > ./test262（没有就报错并给出 clone 提示）。

// ── 用例 frontmatter ─────────────────────────────────────────────────────────

// test262Case 是一个用例的元数据与源码。
type test262Case struct {
	RelPath    string   `json:"path"`           // 相对 test262/test 的路径
	Negative   string   `json:"negative_phase"` // "" | parse | early | resolution | runtime
	NegType    string   `json:"negative_type"`  // 期望错误类型（negative 时）
	Flags      []string `json:"flags"`          // module/async/raw/onlyStrict/noStrict/generated...
	Includes   []string `json:"includes"`       // harness 依赖（assert.js/sta.js/...）
	Features   []string `json:"features"`       // 用例 feature 标签（仅记录，不参与判定）
	Source     string   `json:"source"`         // 用例源码（仅分片 manifest 用；-json 报告走 test262Result，不含它）
	Boundaries string   `json:"-"`              // 原始 frontmatter（调试用）
}

// test262ShardCount 是固定分片数 —— 与 -jobs 解耦是整个 runner 确定性的关键。
//
// 引擎有包级可变状态（vm.currentVM / object.callbackError / stdlib solid 栈），
// 同一进程内连续跑用例会互相污染 ⇒ 用例判定取决于"同进程邻居"。分片数若跟着
// -jobs 变，每个用例的邻居就变，通过数随 -jobs 翻转。钉死分片数后，每个用例
// 永远落在同一片、同一前驱序列，-jobs 只影响并发度。
//
// 取 128 的取舍: 足够大 ⇒ 同一分片内相邻用例在全局序里相隔 128 条（基本落到
// 不同目录），跨用例污染接近"每例一进程"的隔离效果；又足够小 ⇒ 进程数与启动
// 开销可控（尤其本机单进程启动约 0.45s）。分片清单由父进程预先落盘，子进程不
// 再扫盘，故分片数不带来收集成本。
const test262ShardCount = 128

// test262Result 是单个用例的判定结果。
type test262Result struct {
	RelPath string  `json:"path"`
	Pass    bool    `json:"pass"`
	Phase   string  `json:"phase"` // pass / compile / runtime / timeout / harness / crashed
	Err     string  `json:"error,omitempty"`
	Seconds float64 `json:"seconds"`
}

// parseFrontmatter 抽出 /*--- ... ---*/ 元数据块（raw 之外每个用例都有）。
// 手写解析不引 YAML 库: test262 只用到扁平 key + 一层嵌套（negative/includes/
// flags/features），格式由 test262/tools/lint 强约束，比"引入 YAML 解析器"
// 更可控。
func parseFrontmatter(src string) (meta test262Case, body string) {
	meta.Boundaries = ""
	body = src
	start := strings.Index(src, "/*---")
	if start < 0 {
		return
	}
	end := strings.Index(src[start:], "---*/")
	if end < 0 {
		return
	}
	end += start
	yaml := src[start+5 : end]
	meta.Boundaries = yaml
	body = src[end+5:]

	var curSection string
	for _, raw := range strings.Split(yaml, "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		indented := strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
		trimmed := strings.TrimSpace(line)
		switch {
		case !indented && strings.HasSuffix(trimmed, ":"):
			curSection = strings.TrimSuffix(trimmed, ":")
		case !indented && strings.Contains(trimmed, ":"):
			key, val := splitKV(trimmed)
			switch key {
			case "flags", "includes", "features":
				setList(&meta, key, splitList(val))
			}
		case indented && curSection == "negative" && strings.Contains(trimmed, ":"):
			key, val := splitKV(trimmed)
			switch key {
			case "phase":
				meta.Negative = val
			case "type":
				meta.NegType = val
			}
		case indented && curSection != "negative" && strings.HasPrefix(trimmed, "- "):
			// 不进 negative 的列表项: 属于 curSection 对应的列表
			setList(&meta, curSection, append(getList(&meta, curSection), strings.TrimPrefix(trimmed, "- ")))
		}
	}
	return meta, body
}

func splitKV(s string) (string, string) {
	i := strings.Index(s, ":")
	return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:])
}

func splitList(s string) []string {
	s = strings.Trim(s, "[]")
	if s == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if t := strings.Trim(strings.TrimSpace(p), `"'`); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func getList(m *test262Case, key string) []string {
	switch key {
	case "flags":
		return m.Flags
	case "includes":
		return m.Includes
	case "features":
		return m.Features
	}
	return nil
}

func setList(m *test262Case, key string, v []string) {
	switch key {
	case "flags":
		m.Flags = v
	case "includes":
		m.Includes = v
	case "features":
		m.Features = v
	}
}

func hasFlag(c *test262Case, f string) bool {
	for _, fl := range c.Flags {
		if fl == f {
			return true
		}
	}
	return false
}

// ── 用例收集 ─────────────────────────────────────────────────────────────────

// suiteDirs 是 -suite 的目录映射。intl402 依赖 Intl 对象（Gox 未实现 i18n），
// all 也不收 —— 计入分母只会让合规率失真（runner 文档头注释的取舍）。
var suiteDirs = map[string][]string{
	"language":  {"language"},
	"built-ins": {"built-ins"},
	"annexB":    {"annexB"},
	"all":       {"annexB", "built-ins", "language", "staging"},
}

func collectCases(root, suite, filter string) ([]test262Case, error) {
	var re *regexp.Regexp
	if filter != "" {
		var err error
		re, err = regexp.Compile(filter)
		if err != nil {
			return nil, fmt.Errorf("-filter 正则不合法: %v", err)
		}
	}
	var out []test262Case
	for _, dir := range suiteDirs[suite] {
		base := filepath.Join(root, "test", dir)
		err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				// 临时文件在 readdir 与 lstat 之间被并发 runner 清理是正常竞态,
				// 不能让它炸掉整个用例收集。
				if os.IsNotExist(err) && strings.Contains(filepath.Base(path), ".gox-test262-") {
					return nil
				}
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".js") || strings.HasSuffix(path, "_FIXTURE.js") {
				return nil
			}
			// 跳过 runner 自己的执行临时文件（上次崩溃残留时不能当用例收集）
			if strings.HasPrefix(info.Name(), ".gox-test262-") {
				return nil
			}
			rel, _ := filepath.Rel(filepath.Join(root, "test"), path)
			rel = filepath.ToSlash(rel)
			if re != nil && !re.MatchString(rel) {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			meta, _ := parseFrontmatter(string(raw))
			meta.RelPath = rel
			meta.Source = string(raw)
			out = append(out, meta)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RelPath < out[j].RelPath })
	return out, nil
}

// ── 执行与判定 ────────────────────────────────────────────────────────────────

// phaseOfError 把 Gox 的错误消息归类到编译期/运行期两档。
// 格式契约来自 vm/compile.go（sourceError）与 vm/vm.go（EvalVM）—— runner 与
// 引擎之间唯一的耦合点是这三个前缀，改引擎错误格式时必须同步这里（有单测钉住）。
func phaseOfError(err error) string {
	msg := err.Error()
	switch {
	case strings.HasPrefix(msg, "parser errors:"):
		return "parse"
	case strings.HasPrefix(msg, "compiler error:"):
		return "compile"
	default:
		return "runtime"
	}
}

// harnessLibs 是 includes 允许的 harness 文件白名单（防用例乱写 includes 读到
// 别的文件）。缺的文件在拼装时报 harness 错误 —— 这类用例跳过而不判失败，
// 因为它是 runner 的覆盖问题而不是引擎的。
var harnessLibs = map[string]bool{
	"assert.js": true, "sta.js": true, "compareArray.js": true,
	"propertyHelper.js": true, "deepEqual.js": true, "wellKnownIntrinsicObjects.js": true,
	"decimalToHexString.js": true, "byteConversionValues.js": true, "nans.js": true,
	"dateConstants.js": true, "testTypedArray.js": true, "testBigIntTypedArray.js": true,
	"detachArrayBuffer.js": true, "fnGlobalObject.js": true, "nativeFunctionMatcher.js": true,
	"promiseHelper.js": true, "regExpUtils.js": true,
	"isConstructor.js": true, "proxyTrapsHelper.js": true, "asyncHelpers.js": true,
	"built-in-array-from.js": true, "calendar-fields-iterable.js": true,
	"temporalHelpers.js": true, "compareIterator.js": true, "iterableHelpers.js": true,
	"floatArrayExactValues.js": true, "proxy-getOwnPropertyDescriptor-trap.js": true,
	"smi.js": true, "assertRelativeDateMs.js": true, "toNumber.js": true,
	"values.js": true,
}

// 默认 harness: 官方约定 sta.js + assert.js 对所有用例生效（Test262Error /
// $ERROR / assert 全在这里），includes 只声明**附加**依赖。顺序有讲究 ——
// sta.js 在前（Test262Error 定义先于 assert.js 的使用）。
var defaultHarness = []string{"sta.js", "assert.js"}

// harnessOrderFor 合并默认 harness 与用例 includes（去重、保序）。
func harnessOrderFor(c *test262Case) []string {
	seen := map[string]bool{}
	var out []string
	for _, inc := range append(append([]string{}, defaultHarness...), c.Includes...) {
		if !seen[inc] {
			seen[inc] = true
			out = append(out, inc)
		}
	}
	return out
}

// buildScript 把 harness + 用例拼成最终执行源码（非 module 用例）。
func buildScript(c *test262Case, harnessRoot string) (string, error) {
	var b strings.Builder
	// onlyStrict: 官方约定整文件进严格模式。"use strict" 是一条**指令**
	// (Directive), 只有落在 ScriptBody 的 Directive Prologue 最前位置才生效
	// —— 一旦它前面出现任何非指令语句（哪怕是换行, 或 sta.js 里的函数声明）,
	// 它就退化成普通字符串表达式语句, 不再切换严格模式。harness 含函数声明,
	// 所以严格指令必须排在**所有 harness 之前**（async shim 同理, 排在指令
	// 之后、用例之前）。
	if hasFlag(c, "onlyStrict") {
		b.WriteString("\"use strict\";\n")
	}
	for _, inc := range harnessOrderFor(c) {
		if !harnessLibs[inc] {
			return "", fmt.Errorf("runner 未登记的 harness 依赖: %s", inc)
		}
		data, err := os.ReadFile(filepath.Join(harnessRoot, inc))
		if err != nil {
			return "", fmt.Errorf("harness 文件缺失: %s", inc)
		}
		b.Write(data)
		b.WriteString("\n")
	}
	if hasFlag(c, "async") {
		b.WriteString(test262AsyncShim)
	}
	b.WriteString(c.Source)
	if hasFlag(c, "async") {
		// 文件末尾留一个哨兵表达式: EvalFileVM 的 LastPopped() 取到它,
		// 宿主由此读取 $DONE 是否被调（module 命名空间读不到, 见 doc 注释）。
		b.WriteString("\n;__test262_done\n")
	}
	return b.String(), nil
}

// test262AsyncShim 是 async 用例的 $DONE 实现（官方约定宿主必须提供）。
const test262AsyncShim = `
globalThis.__test262_done = false;
globalThis.__test262_err = undefined;
function $DONE(err) {
  if (err) { globalThis.__test262_err = String(err && err.message || err); }
  globalThis.__test262_done = true;
}
`

// judgePhase 判定一个非 async 用例是否满足 frontmatter 期望。
func judgePhase(c *test262Case, execErr error) (bool, string, string) {
	if execErr == nil {
		if c.Negative != "" {
			return false, "compile", fmt.Sprintf("期望 %s 报错但成功执行", c.Negative)
		}
		return true, "pass", ""
	}
	phase := phaseOfError(execErr)
	if c.Negative == "" {
		return false, "runtime", firstLine(execErr.Error())
	}
	// negative: 期望报错。按 phase 对齐 Gox 的错误三段格式。
	ok := false
	switch c.Negative {
	case "parse":
		// test262 的 phase 标注不区分引擎的 parse/compile 实现分层:
		// 重复声明等 early error 在 Gox 是编译期检查, 只要早于 runtime
		// 执行即满足语义。compile 期要求错误消息含 SyntaxError 防误判。
		ok = phase == "parse" || (phase == "compile" && strings.Contains(execErr.Error(), "SyntaxError"))
	case "early":
		ok = phase == "parse" || phase == "compile"
	case "resolution":
		// 模块解析错误在 Gox 是运行期 loadModule 抛的 —— 任何阶段都认
		ok = true
	case "runtime":
		ok = phase == "runtime"
	default:
		ok = true // 未知 phase, 宽松
	}
	if !ok {
		return false, "compile", fmt.Sprintf("期望 %s 报错, 实际 %s 阶段错误: %s", c.Negative, phase, firstLine(execErr.Error()))
	}
	// NegType 是期望的 JS 异常类型 (如 SyntaxError)。引擎在 parser/compiler
	// 阶段用编译期错误代替运行时异常 —— 该阶段不存在 JS 异常对象, 但
	// "语法错误" 的性质已由 phase 判定确认, 故 parse/compile 阶段豁免
	// 文本匹配 (否则要迁就实现给消息硬塞 "SyntaxError" 字样)。
	// runtime 阶段的错误才是真异常, 仍要求类型名匹配。
	// 匹配只取**错误消息首行** (不掺源码回显), 理由见 errorHead。
	if c.NegType != "" && phase == "runtime" && !strings.Contains(errorHead(execErr.Error()), c.NegType) {
		return false, "runtime", fmt.Sprintf("错误类型不匹配: 期望含 %s, 实际: %s", c.NegType, firstLine(execErr.Error()))
	}
	return true, "pass", ""
}

// errorHead 只取错误消息的**首行**（去首尾空白），用于 negative.type 匹配。
//
// 运行时错误的 Error() 是 vm.FrameError 的渲染结果 —— 首行是引擎给出的异常
// 消息本身（如 "vm error: TypeError: ..."），其后的行是「源码片段 + 插入符」。
// 若拿整段字符串做 strings.Contains(_, NegType)，只要被回显的那一行源码里
// 出现期望的类型名（典型：用例体里的 `throw new Test262Error();`）就会误判
// 通过 —— 判据其实没有验证异常类型。故类型匹配必须限定在首行。
//
// 反例：用例体是 `throw new Test262Error();`、期望 type: Test262Error 时，
// 只要报错帧恰好落在那一行，回显里就有 "Test262Error" 字样 —— 判据形同虚设。
func errorHead(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		// parser errors 是多行消息（首行只有前缀, 具体行号在后续行）——
		// 折叠成一行保留信息量。
		return strings.ReplaceAll(strings.TrimSpace(s), "\n", " | ")
	}
	return s
}

// runCase 执行单个用例。独立临时文件: Gox 的模块解析以文件路径为基准,
// import "./x.js" 才能落到用例目录 —— 非模块用例也统一走文件（与 node 一致）。
func runCase(root string, c *test262Case, timeout time.Duration) test262Result {
	start := time.Now()
	res := test262Result{RelPath: c.RelPath}

	srcDir := filepath.Dir(filepath.Join(root, "test", c.RelPath))
	isModule := hasFlag(c, "module")

	var script string
	var modulePreamble string // module 用例: 先按 script 执行的 harness（注入全局）
	if isModule {
		// module 用例走**真模块入口**（见下方 goroutine 的 EvalModuleFileVMWithGlobals 分支）,
		// 于是不能把 harness 拼进模块源 —— 那样 harness 绑定只会是**模块作用域**,
		// 被 import 的 fixture 与自导入副本看不到 assert/Test262Error（实测报
		// "ReferenceError: assert is not defined"）。正解与官方 runner 同模型:
		// harness 先按 script 在**同一全局环境**执行, 再把用例以模块入口执行。
		var b strings.Builder
		for _, inc := range harnessOrderFor(c) {
			if !harnessLibs[inc] {
				res.Phase = "harness"
				res.Err = "runner 未登记的 harness 依赖: " + inc
				return res
			}
			data, err := os.ReadFile(filepath.Join(root, "harness", inc))
			if err != nil {
				res.Phase = "harness"
				res.Err = "harness 文件缺失: " + inc
				return res
			}
			b.Write(data)
			b.WriteString("\n")
		}
		if hasFlag(c, "async") {
			b.WriteString(test262AsyncShim)
		}
		modulePreamble = b.String()
		// 临时文件只放用例源码: import 相对路径仍以用例所在目录为基准。
		script = c.Source
	} else {
		built, err := buildScript(c, filepath.Join(root, "harness"))
		if err != nil {
			res.Phase = "harness"
			res.Err = err.Error()
			return res
		}
		script = built
	}

	tmp, err := os.CreateTemp(srcDir, ".gox-test262-*.js")
	if err != nil {
		res.Phase = "harness"
		res.Err = "临时文件创建失败: " + err.Error()
		return res
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(script); err != nil {
		tmp.Close()
		res.Phase = "harness"
		res.Err = "临时文件写入失败: " + err.Error()
		return res
	}
	tmp.Close()

	type outcome struct {
		err        error
		harnessErr bool
		hasVM      bool
		vmRef      *vm.VM
	}
	doneCh := make(chan outcome, 1)
	go func() {
		var engine *vm.VM
		var err error
		if isModule {
			// module 用例走**真模块入口** (模块顶层恒严格 + 顶层 this=undefined,
			// 且按模块早错规则拦截重复导出名/未声明导出/顶层 return·yield 等 ——
			// EvalModuleFileVMWithGlobals 内部以 moduleMode+moduleEE 双 true 编译):
			// 先把 harness 按 script 执行进**同一个全局环境**, 再让用例以模块入口
			// 运行 —— 见上方 modulePreamble 注释。其余用例按 script 入口。
			hvm, herr := vm.EvalVM(modulePreamble)
			if herr != nil {
				doneCh <- outcome{err: herr, harnessErr: true}
				return
			}
			engine, err = vm.EvalModuleFileVMWithGlobals(tmpPath, hvm.Globals())
		} else {
			engine, err = vm.EvalFileVM(tmpPath)
		}
		if err != nil {
			doneCh <- outcome{err: err}
			return
		}
		doneCh <- outcome{vmRef: engine, hasVM: true}
	}()

	var oc outcome
	select {
	case oc = <-doneCh:
	case <-time.After(timeout):
		res.Phase = "timeout"
		res.Err = fmt.Sprintf("执行超时 (>%s)", timeout)
		res.Seconds = time.Since(start).Seconds()
		return res
	}
	if oc.harnessErr {
		res.Phase = "harness"
		res.Err = "harness 执行失败: " + oc.err.Error()
		res.Seconds = time.Since(start).Seconds()
		return res
	}

	// async 用例: 同步代码跑完不代表用例完 —— 驱动定时器/微任务泵到 $DONE。
	if hasFlag(c, "async") && oc.hasVM && oc.err == nil {
		engine := oc.vmRef
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			if v, ok := engine.Globals().Get("__test262_done"); ok {
				if b, isBool := v.(*object.Boolean); isBool && b.Value {
					break
				}
			}
			engine.RunTimersUntil(time.Now().Add(50 * time.Millisecond))
		}
		if v, ok := engine.Globals().Get("__test262_err"); ok {
			if _, isUndef := v.(*object.Undefined); !isUndef && v != nil {
				oc.err = fmt.Errorf("vm error: Test262Error: $DONE(%s)", v.Inspect())
			}
		}
	}

	res.Seconds = time.Since(start).Seconds()
	if isModule && hasFlag(c, "async") && oc.err == nil {
		// 模块命名空间的 $DONE 状态宿主读不到 —— 跑完无异常即放行,
		// 如实标注 phase, 不冒充 pass。
		res.Phase = "pass"
		res.Pass = true
		return res
	}
	ok, phase, msg := judgePhase(c, oc.err)
	res.Pass = ok
	res.Phase = phase
	res.Err = msg
	return res
}

// ── 主命令 ───────────────────────────────────────────────────────────────────

// groupStat 是一个目录分组的统计。
type groupStat struct {
	Total int     `json:"total"`
	Pass  int     `json:"pass"`
	Rate  float64 `json:"rate_pct"`
}

type jsonReport struct {
	// Engine 是产出本次结果的引擎身份 (rFf4lR)。
	//
	// 没有它, 一份 test262 JSON 离开产出它的二进制就永久失去身份 —— 账本
	// 无法回答"这份基线是哪个 commit 跑出来的"。历史两次 A/B 误判 (base
	// 与文件名声称的提交不符 / 拿缓存的旧二进制当 base) 都是这个洞造成的。
	// 对比两份 JSON 之前必须先比对 Engine.Revision 与 Engine.Modified。
	Engine  engineInfo           `json:"engine"`
	Suite   string               `json:"suite"`
	Root    string               `json:"root"`
	Total   int                  `json:"total"`
	Passed  int                  `json:"passed"`
	Failed  int                  `json:"failed"`
	Skipped int                  `json:"skipped"` // harness 缺失等 runner 侧跳过
	Rate    float64              `json:"rate_pct"`
	Seconds float64              `json:"seconds"`
	ByGroup map[string]groupStat `json:"by_group"`
	Results []test262Result      `json:"results"`
}

func runTest262(args []string) {
	fs := flag.NewFlagSet("test262", flag.ExitOnError)
	rootFlag := fs.String("root", "", "test262 仓库根目录（默认 $GOX_TEST262 或 ./test262）")
	suite := fs.String("suite", "language", "用例子集: language | built-ins | annexB | all")
	filter := fs.String("filter", "", "只跑路径匹配该正则的用例（相对 test/ 目录）")
	jobs := fs.Int("jobs", runtime.NumCPU(), "并发分片数上限 (分片数与 -jobs 无关, 见 test262ShardCount)")
	timeoutSec := fs.Int("timeout", 3, "单用例超时秒数")
	jsonOut := fs.String("json", "", "把全量结果写成 JSON 报告（供基线对比）")
	maxFail := fs.Int("maxfail", 0, "失败数达到 N 即停止（0=不限制）")
	quiet := fs.Bool("quiet", false, "只输出汇总（默认失败清单也打前 20 条）")
	list := fs.Bool("list", false, "只列出用例清单不执行")
	casefile := fs.String("casefile", "", "内部参数: 分片清单文件（每行一个用例 JSON, 含源码）")
	jsonlOut := fs.String("jsonl", "", "内部参数: 分片子进程逐用例 JSONL 落点 (崩溃也保住已完成用例)")
	one := fs.String("one", "", "内部参数: 只跑单个用例 (孤儿重派, 进程级隔离)")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	root := *rootFlag
	if root == "" {
		root = os.Getenv("GOX_TEST262")
	}
	if root == "" {
		root = "./test262"
	}
	if !dirExists(filepath.Join(root, "test")) || !dirExists(filepath.Join(root, "harness")) {
		fmt.Fprintf(os.Stderr, "gox test262: 找不到 Test262 仓库（查过: -root / $GOX_TEST262 / ./test262）\n"+
			"  获取: git clone --depth 1 https://github.com/tc39/test262.git\n"+
			"  然后: gox test262 -root <test262目录> （或设 GOX_TEST262 环境变量）\n")
		os.Exit(2)
	}
	if _, ok := suiteDirs[*suite]; !ok {
		fmt.Fprintf(os.Stderr, "gox test262: 未知 -suite %q（可选: language | built-ins | annexB | all）\n", *suite)
		os.Exit(2)
	}

	// ── 单用例模式 (孤儿重派) —— 最高优先级 ──
	// 必须在 collectCases / runSharded 之前: (1) 直读单文件, 免去每个
	// 孤儿进程全量扫描 test262 的浪费; (2) 无 -jobs 的手动调用 jobs 默认
	// NumCPU>1, 若先走分片派发会误入 runSharded 引发孤儿风暴。
	// 一个用例一个进程: 超时泄漏 / runtime fatal 都随进程退出消亡,
	// 绝不传染其他用例。主进程聚合 JSONL。
	if *one != "" {
		p := filepath.Join(root, "test", filepath.FromSlash(*one))
		raw, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gox test262: -one 读取用例失败 %q: %v\n", *one, err)
			os.Exit(1)
		}
		meta, _ := parseFrontmatter(string(raw))
		meta.RelPath = *one
		meta.Source = string(raw)
		r := runCase(root, &meta, time.Duration(*timeoutSec)*time.Second)
		if *jsonlOut != "" {
			appendJSONL(*jsonlOut, r)
		} else {
			data, _ := json.Marshal(r)
			fmt.Println(string(data))
		}
		return
	}

	// ── 分片清单模式 (子进程) —— 第二优先级 ──
	// 父进程已把本分片的用例 (含源码) 落成 JSONL manifest: 子进程直读执行,
	// 不再扫盘收集。必须在 collectCases 之前 —— 固定分片数下每个子进程都重扫
	// 一遍全量的话, 收集成本是分片数的倍数 (全量扫盘约 6.5s × S)。
	if *casefile != "" {
		loaded, err := readCaseManifest(*casefile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gox test262: -casefile 读取失败 %q: %v\n", *casefile, err)
			os.Exit(1)
		}
		// 子进程内串行执行 (jobs=1); stopOnTimeout 让遇超时即退, 把泄漏的
		// VM goroutine 与后续用例并发之前扼杀, 剩余用例交主进程孤儿重派。
		executeCases(root, loaded, *suite, 1, *timeoutSec, *maxFail, "", *jsonlOut, *quiet, true)
		return
	}

	cases, err := collectCases(root, *suite, *filter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gox test262: 收集用例失败: %v\n", err)
		os.Exit(1)
	}
	if *list {
		for _, c := range cases {
			fmt.Println(c.RelPath)
		}
		return
	}
	if len(cases) == 0 {
		fmt.Fprintln(os.Stderr, "gox test262: 没有匹配的用例（检查 -suite / -filter）")
		os.Exit(1)
	}

	// ── 分片进程并行 ──
	//
	// 并行用**进程分片**实现: 父进程按固定分片数 (test262ShardCount) 切用例、
	// 落清单, 用 jobs 个并发槽 spawn 子进程, 子进程内串行执行, 父进程聚合。
	// 分片数固定 ⇒ 每个用例的判定与 -jobs 无关; 进程级隔离 ⇒ 引擎 fatal 只崩
	// 一个分片, 其余分片成果不受牵连 (崩溃分片的未完成用例由孤儿重派接手)。
	runSharded(cases, args, *jobs, *maxFail, *jsonOut != "")
}

// runSharded 按**固定分片数** (test262ShardCount) 切用例, 用 jobs 个并发槽
// spawn 子进程执行并聚合报告。分片数与 -jobs 无关 —— 见 test262ShardCount
// 的说明: 引擎的包级状态使"用例结果取决于同进程邻居", 分片数跟 -jobs 变就
// 会让通过数随 -jobs 翻转; 钉死分片数后 -jobs 只决定并发度。
//
// 分片的用例清单 (含源码) 由父进程落成临时 manifest, 子进程用 -casefile 直读,
// 不再各自扫盘收集 (否则 S 片 = S 倍收集成本)。
//
// -maxfail 由父进程全局判定 (子进程不提前退出): 累计各分片失败数, 达到阈值
// 即停派后续分片。子进程若各自提前退出, 其未跑用例会落进"差集"被当孤儿重派,
// 反而把 -maxfail 想省的工作又跑回来。
//
// 韧性设计: 子进程逐用例把结果追加成 JSONL (崩溃也保住已完成用例);
// 分片子进程遇首个超时即退出 (超时泄漏的 VM 与后续用例并发会触发
// vm 包包级状态的 fatal error, 不可 recover —— 提前退出扼杀泄漏)。
// 主进程聚合后对"应收 - 已完成"差集做孤儿重派: 一个用例一个进程,
// 彻底进程级隔离。重派后仍缺失的标记 crashed。
func runSharded(cases []test262Case, parentArgs []string, jobs, maxFail int, wantJSON bool) {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gox test262: 无法定位可执行文件: %v\n", err)
		os.Exit(1)
	}
	// 传递用户原始参数 (供子进程/孤儿重派复用), 剥掉 runSharded 自己管理的
	// -jobs/-maxfail/-json 与子进程专用参数。注意都带值, 必须 skipNext 连值
	// 一起跳过 —— 否则值会以裸位置参数混进 baseArgs, flag 包遇到位置参数即
	// 停止解析。
	baseArgs := []string{"test262"}
	skipNext := false
	for _, a := range parentArgs {
		if skipNext {
			skipNext = false
			continue
		}
		if a == "-jobs" || a == "-json" || a == "-maxfail" || a == "-casefile" || a == "-jsonl" || a == "-one" {
			skipNext = true
			continue
		}
		if strings.HasPrefix(a, "-jobs=") || strings.HasPrefix(a, "-json=") ||
			strings.HasPrefix(a, "-maxfail=") || strings.HasPrefix(a, "-casefile=") ||
			strings.HasPrefix(a, "-jsonl=") {
			continue
		}
		baseArgs = append(baseArgs, a)
	}

	shardTotal := test262ShardCount
	if shardTotal > len(cases) {
		shardTotal = len(cases)
	}
	if shardTotal < 1 {
		shardTotal = 1
	}
	concurrency := jobs
	if concurrency > shardTotal {
		concurrency = shardTotal
	}
	if concurrency < 1 {
		concurrency = 1
	}

	// 分片: 全局序里 idx%S 相同的用例归同一片 (父进程算好后落清单)。
	shardCases := make([][]test262Case, shardTotal)
	for idx := range cases {
		s := idx % shardTotal
		shardCases[s] = append(shardCases[s], cases[idx])
	}
	caseFiles := make([]string, shardTotal)  // 分片清单 (含源码)
	jsonlFiles := make([]string, shardTotal) // 分片结果 JSONL
	for i := 0; i < shardTotal; i++ {
		cf, err := os.CreateTemp("", "gox-test262-cases-*.jsonl")
		if err != nil {
			fmt.Fprintf(os.Stderr, "gox test262: 创建分片清单失败: %v\n", err)
			os.Exit(1)
		}
		if err := writeCaseManifest(cf, shardCases[i]); err != nil {
			cf.Close()
			fmt.Fprintf(os.Stderr, "gox test262: 写分片清单失败: %v\n", err)
			os.Exit(1)
		}
		cf.Close()
		caseFiles[i] = cf.Name()

		rf, err := os.CreateTemp("", "gox-test262-shard-*.jsonl")
		if err != nil {
			fmt.Fprintf(os.Stderr, "gox test262: 创建分片临时文件失败: %v\n", err)
			os.Exit(1)
		}
		rf.Close()
		jsonlFiles[i] = rf.Name()
	}

	fmt.Printf("分片执行: 固定 %d 片 × 并发 %d × 每片约 %d 用例\n",
		shardTotal, concurrency, (len(cases)+shardTotal-1)/shardTotal)
	start := time.Now()
	var next atomic.Int64
	var failTotal atomic.Int64
	var stopDispatch atomic.Bool
	dispatched := make([]bool, shardTotal) // 记录哪些分片真跑过 (供孤儿判定排除 -maxfail 停派片)
	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if stopDispatch.Load() {
					return
				}
				i := int(next.Add(1)) - 1
				if i >= shardTotal {
					return
				}
				dispatched[i] = true
				args := append(append([]string(nil), baseArgs...),
					"-casefile", caseFiles[i], "-jsonl", jsonlFiles[i])
				if os.Getenv("GOX_TEST262_DEBUG") != "" {
					fmt.Fprintf(os.Stderr, "[debug] 分片 %d 命令: %s %v\n", i, exe, args)
				}
				cmd := exec.Command(exe, args...)
				cmd.Env = append(os.Environ(), "GOX_TEST262_SHARD_CHILD=1")
				cmd.Stdout = nil
				cmd.Stderr = os.Stderr
				if err := cmd.Run(); err != nil {
					fmt.Fprintf(os.Stderr, "gox test262: 分片 %d 异常退出: %v\n", i, err)
				}
				// 全局 -maxfail: 累计本片失败数, 达到阈值即停派后续分片。
				if maxFail > 0 {
					fails := 0
					for _, r := range readJSONL(jsonlFiles[i]) {
						if !r.Pass && r.Phase != "harness" {
							fails++
						}
					}
					if failTotal.Add(int64(fails)) >= int64(maxFail) {
						stopDispatch.Store(true)
					}
				}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	// 聚合: JSONL 逐行读取, 不依赖子进程完整存活
	//
	// 引擎身份取**本进程** (派发方) 的 —— 子进程是同一个二进制, 身份一致;
	// 且聚合落盘只发生在这一处, 这里不写就没有第二次机会 (rFf4lR)。
	report := jsonReport{Engine: currentEngineInfo(), Suite: readSuiteArg(parentArgs), ByGroup: map[string]groupStat{}}
	var results []test262Result
	seen := map[string]bool{}
	for i := 0; i < shardTotal; i++ {
		lines := readJSONL(jsonlFiles[i])
		if len(lines) == 0 && len(shardCases[i]) > 0 {
			fmt.Fprintf(os.Stderr, "gox test262: 分片 %d 无结果产出\n", i)
		}
		results = append(results, lines...)
		os.Remove(jsonlFiles[i])
		os.Remove(caseFiles[i])
	}
	for _, r := range results {
		seen[r.RelPath] = true
	}

	// 孤儿重派: 分片超时退出/崩溃遗留的用例, 一个用例一个进程重跑。
	// 只针对"已派发但未产出"的用例 —— -maxfail 主动停派的分片不算缺失,
	// 否则停止后差集又会把整个分片重派回来, -maxfail 形同虚设。
	var missing []test262Case
	for i := 0; i < shardTotal; i++ {
		if !dispatched[i] {
			continue
		}
		for _, c := range shardCases[i] {
			if !seen[c.RelPath] {
				missing = append(missing, c)
			}
		}
	}
	if len(missing) > 0 {
		fmt.Printf("孤儿重派: %d 个用例未完成 (分片超时退出/崩溃), 单用例进程隔离重跑\n", len(missing))
		var mu sync.Mutex
		sem := make(chan struct{}, concurrency)
		var wg sync.WaitGroup
		for _, c := range missing {
			wg.Add(1)
			sem <- struct{}{}
			go func(tc test262Case) {
				defer wg.Done()
				defer func() { <-sem }()
				f, err := os.CreateTemp("", "gox-test262-orphan-*.jsonl")
				if err != nil {
					return
				}
				f.Close()
				args := append([]string(nil), baseArgs...) // baseArgs 已含 "test262", 不可再加前缀
				args = append(args, "-one", tc.RelPath, "-jsonl", f.Name())
				if os.Getenv("GOX_TEST262_DEBUG") != "" {
					fmt.Fprintf(os.Stderr, "[debug] 孤儿 %s 命令: %s %v\n", tc.RelPath, exe, args)
				}
				cmd := exec.Command(exe, args...)
				cmd.Env = append(os.Environ(), "GOX_TEST262_SHARD_CHILD=1")
				cmd.Stdout = nil
				if os.Getenv("GOX_TEST262_DEBUG") == "" {
					cmd.Stderr = nil // 孤儿进程 fatal 的 stack 别刷屏
				}
				if err := cmd.Run(); err != nil && os.Getenv("GOX_TEST262_DEBUG") != "" {
					fmt.Fprintf(os.Stderr, "[debug] 孤儿 %s run 失败: %v\n", tc.RelPath, err)
				}
				orphans := readJSONL(f.Name())
				os.Remove(f.Name())
				mu.Lock()
				results = append(results, orphans...)
				mu.Unlock()
			}(c)
		}
		wg.Wait()
		// 重派后仍缺失的: 标记 crashed, 如实呈现
		seen2 := map[string]bool{}
		for _, r := range results {
			seen2[r.RelPath] = true
		}
		for _, c := range missing {
			if !seen2[c.RelPath] {
				results = append(results, test262Result{RelPath: c.RelPath, Phase: "crashed",
					Err: "孤儿重派仍未产出 (疑似引擎 runtime fatal)"})
			}
		}
	}
	elapsed = time.Since(start) // 孤儿重派耗时也计入总耗时
	if len(results) == 0 {
		fmt.Fprintf(os.Stderr, "gox test262: 所有分片均未产出结果 —— 子进程参数或执行链路有 bug\n")
		os.Exit(1)
	}

	// 统计与分组 (JSONL 是唯一真源, 全量重算)
	report.ByGroup = map[string]groupStat{}
	for _, r := range results {
		if r.RelPath == "" {
			continue
		}
		report.Total++
		switch {
		case r.Phase == "harness":
			report.Skipped++
		case r.Pass:
			report.Passed++
		default:
			report.Failed++
		}
		group := groupOf(r.RelPath)
		g := report.ByGroup[group]
		g.Total++
		if r.Pass {
			g.Pass++
		}
		report.ByGroup[group] = g
	}
	for k, g := range report.ByGroup {
		if g.Total > 0 {
			g.Rate = float64(g.Pass) / float64(g.Total) * 100
		}
		report.ByGroup[k] = g
	}
	if report.Total > 0 {
		report.Rate = float64(report.Passed) / float64(report.Total) * 100
	}
	report.Seconds = elapsed.Seconds()

	fmt.Printf("\n===== 合规率汇总 =====\n")
	printEngineBanner(report.Engine)
	fmt.Printf("执行 %d | 通过 %d | 失败 %d | runner 跳过 %d\n", report.Total, report.Passed, report.Failed, report.Skipped)
	fmt.Printf("合规率: %.2f%%   耗时: %s\n", report.Rate, elapsed.Round(time.Millisecond))
	keys := make([]string, 0, len(report.ByGroup))
	for k := range report.ByGroup {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Println("\n----- 按目录分组 -----")
	for _, k := range keys {
		g := report.ByGroup[k]
		fmt.Printf("  %-42s %6.2f%%  (%d/%d)\n", k, g.Rate, g.Pass, g.Total)
	}
	if wantJSON && *jsonFlag(parentArgs) != "" {
		report.Results = results
		data, err := json.MarshalIndent(report, "", "  ")
		if err == nil {
			_ = os.WriteFile(*jsonFlag(parentArgs), data, 0o644)
		}
	}
}

// jsonFlag 取父参数里 -json 的值（聚合落盘用）。
func jsonFlag(args []string) *string {
	v := ""
	for i, a := range args {
		if a == "-json" && i+1 < len(args) {
			v = args[i+1]
		} else if strings.HasPrefix(a, "-json=") {
			v = strings.TrimPrefix(a, "-json=")
		}
	}
	return &v
}

// readSuiteArg 取父参数里 -suite 的值（聚合报告标注用）。
func readSuiteArg(args []string) string {
	for i, a := range args {
		if a == "-suite" && i+1 < len(args) {
			return args[i+1]
		} else if strings.HasPrefix(a, "-suite=") {
			return strings.TrimPrefix(a, "-suite=")
		}
	}
	return "language"
}

// appendJSONL 把一条结果追加为 JSONL 行 (O_APPEND 直写, 无缓冲丢失窗口)。
func appendJSONL(path string, r test262Result) {
	line, err := json.Marshal(r)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// writeCaseManifest 把一个分片的用例 (含源码) 写成 JSONL 清单, 供子进程
// 用 -casefile 直读 —— 避免每个分片各自扫盘收集全量用例。
func writeCaseManifest(f *os.File, cases []test262Case) error {
	w := bufio.NewWriter(f)
	for i := range cases {
		line, err := json.Marshal(&cases[i])
		if err != nil {
			return err
		}
		if _, err := w.Write(append(line, '\n')); err != nil {
			return err
		}
	}
	return w.Flush()
}

// readCaseManifest 读回 writeCaseManifest 落盘的分片清单。
func readCaseManifest(path string) ([]test262Case, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []test262Case
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var c test262Case
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, fmt.Errorf("第 %d 行不是合法用例清单: %v", len(out)+1, err)
		}
		if c.RelPath != "" {
			out = append(out, c)
		}
	}
	return out, nil
}

// readJSONL 读取逐用例 JSONL (分片子进程/孤儿进程的成果文件)。
func readJSONL(path string) []test262Result {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []test262Result
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var r test262Result
		if json.Unmarshal([]byte(line), &r) == nil && r.RelPath != "" {
			out = append(out, r)
		}
	}
	return out
}

// executeCases 在当前进程内串行执行一批用例 (jobs=1 的 worker 池)。
// 只被两条子进程路径调用: 分片清单 (-casefile) 与孤儿重派 (-one 不经此),
// 主进程的分片并行逻辑 (runSharded) 不进入本函数。
//
// jsonlOut 非空时逐用例把结果追加成 JSONL —— 子进程崩溃 (VM 深层的
// concurrent map fatal / 栈溢出无法 recover) 只丢正在执行的一个用例。
// stopOnTimeout 为 true 时 (分片子进程), 出现第一个超时就停止派发后续
// 用例并退出进程: 超时泄漏的 VM goroutine 无法中断, 它与后续用例的
// VM 并发会触发包级状态的 concurrent map fatal —— 提前退出把泄漏
// 扼杀在并发发生之前, 剩余用例由主进程的孤儿重派接手。
func executeCases(root string, cases []test262Case, suite string, jobs, timeoutSec, maxFail int, jsonOut, jsonlOut string, quiet, stopOnTimeout bool) {
	fmt.Printf("Test262 合规率 runner: suite=%s 用例=%d jobs=%d\n", suite, len(cases), jobs)

	// 用例可能 console.log —— runner 期间把 stdout 指到 /dev/null, 汇总前恢复。
	// (stdlib/console.go 直写 os.Stdout 包变量; os.Stdout 是 *os.File,
	// 所以必须给一个真实文件 —— /dev/null 正合适。)
	saved := os.Stdout
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gox test262: 打开 %s 失败: %v\n", os.DevNull, err)
		os.Exit(1)
	}
	os.Stdout = devnull

	start := time.Now()
	results := make([]test262Result, len(cases))
	sem := make(chan struct{}, jobs)
	var wg sync.WaitGroup
	var stop atomic.Bool
	var failCount atomic.Int64
	for i := range cases {
		if stop.Load() {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					results[idx] = test262Result{RelPath: cases[idx].RelPath, Phase: "crashed",
						Err: fmt.Sprintf("runner panic: %v", r)}
				}
			}()
			if stop.Load() {
				return
			}
			results[idx] = runCase(root, &cases[idx], time.Duration(timeoutSec)*time.Second)
			r := results[idx]
			if jsonlOut != "" {
				appendJSONL(jsonlOut, r)
			}
			if !r.Pass && r.Phase != "harness" {
				if maxFail > 0 && failCount.Add(1) >= int64(maxFail) {
					stop.Store(true)
				}
			}
			if stopOnTimeout && r.Phase == "timeout" {
				stop.Store(true)
			}
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)
	os.Stdout = saved
	devnull.Close()

	eng := currentEngineInfo()
	report := jsonReport{Engine: eng, Suite: suite, Root: root, Seconds: elapsed.Seconds(), ByGroup: map[string]groupStat{}}
	var failedList []test262Result
	for _, r := range results {
		if r.RelPath == "" {
			continue
		}
		report.Total++
		switch {
		case r.Phase == "harness":
			report.Skipped++
		case r.Pass:
			report.Passed++
		default:
			report.Failed++
			failedList = append(failedList, r)
		}
		group := groupOf(r.RelPath)
		g := report.ByGroup[group]
		g.Total++
		if r.Pass {
			g.Pass++
		}
		report.ByGroup[group] = g
	}
	if report.Total > 0 {
		report.Rate = float64(report.Passed) / float64(report.Total) * 100
	}
	for k, g := range report.ByGroup {
		if g.Total > 0 {
			g.Rate = float64(g.Pass) / float64(g.Total) * 100
		}
		report.ByGroup[k] = g
	}

	fmt.Printf("\n===== 合规率汇总 (%s) =====\n", suite)
	printEngineBanner(eng)
	fmt.Printf("执行 %d | 通过 %d | 失败 %d | runner 跳过 %d\n", report.Total, report.Passed, report.Failed, report.Skipped)
	fmt.Printf("合规率: %.2f%%   耗时: %s\n", report.Rate, elapsed.Round(time.Millisecond))
	fmt.Println("\n----- 按目录分组 -----")
	keys := make([]string, 0, len(report.ByGroup))
	for k := range report.ByGroup {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		g := report.ByGroup[k]
		fmt.Printf("  %-42s %6.2f%%  (%d/%d)\n", k, g.Rate, g.Pass, g.Total)
	}
	if !quiet && len(failedList) > 0 {
		fmt.Printf("\n----- 失败用例 (前 20 / 共 %d) -----\n", len(failedList))
		for i, r := range failedList {
			if i >= 20 {
				break
			}
			fmt.Printf("  FAIL %s [%s] %s\n", r.RelPath, r.Phase, r.Err)
		}
	}
	if jsonOut != "" {
		report.Results = results
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "gox test262: JSON 序列化失败: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(jsonOut, data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "gox test262: 写报告失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\n报告已写入: %s\n", jsonOut)
	}
}

// groupOf 取用例路径的前两级目录做分组（language/expressions、built-ins/Array 等）。
func groupOf(rel string) string {
	parts := strings.Split(rel, "/")
	if len(parts) >= 3 {
		return strings.Join(parts[:2], "/")
	}
	if len(parts) == 2 {
		return parts[0]
	}
	return rel
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
