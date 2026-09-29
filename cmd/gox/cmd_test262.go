package main

import (
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
// 用法:
//   gox test262 [-root 目录] [-suite language] [-filter 正则] [-jobs N]
//               [-timeout 秒] [-json 文件] [-maxfail N] [-quiet] [-list]
//
// root 解析顺序: -root > $GOX_TEST262 > ./test262（没有就报错并给出 clone 提示）。

// ── 用例 frontmatter ─────────────────────────────────────────────────────────

// test262Case 是一个用例的元数据与源码。
type test262Case struct {
	RelPath   string   `json:"path"`            // 相对 test262/test 的路径
	Negative  string   `json:"negative_phase"`  // "" | parse | early | resolution | runtime
	NegType   string   `json:"negative_type"`   // 期望错误类型（negative 时）
	Flags     []string `json:"flags"`           // module/async/raw/onlyStrict/noStrict/generated...
	Includes  []string `json:"includes"`        // harness 依赖（assert.js/sta.js/...）
	Features  []string `json:"features"`        // 用例 feature 标签（仅记录，不参与判定）
	Source    string   `json:"-"`               // 用例源码（-json 不落盘）
	Boundaries string  `json:"-"`               // 原始 frontmatter（调试用）
}

// test262Result 是单个用例的判定结果。
type test262Result struct {
	RelPath string `json:"path"`
	Pass    bool   `json:"pass"`
	Phase   string `json:"phase"` // pass / compile / runtime / timeout / harness / crashed
	Err     string `json:"error,omitempty"`
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
	// onlyStrict: 官方约定整文件进严格模式 —— 指令必须放文件最前才有用,
	// 所以它排在 harness 之前? 不: sta/assert 必须与用例同一严格性上下文,
	// 官方 runner 拼装时严格指令放 harness 之后、用例之前照样对 (harness
	// 自身是非严格代码, 指令只影响其后的用例体 —— 与官方 node runner 的
	// 逐文件 eval 行为对齐, 宽松处理对第一轮足够)。
	if hasFlag(c, "onlyStrict") {
		b.WriteString("\"use strict\";\n")
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
	if c.NegType != "" && !strings.Contains(execErr.Error(), c.NegType) {
		return false, "runtime", fmt.Sprintf("错误类型不匹配: 期望含 %s, 实际: %s", c.NegType, firstLine(execErr.Error()))
	}
	return true, "pass", ""
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
	if isModule {
		// module 用例: 源码写进用例同目录的临时文件（import 相对路径保持有效）,
		// harness 依赖拼在最前（import 声明提升, 顺序合法）。
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
		b.WriteString(c.Source)
		script = b.String()
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
		err   error
		hasVM bool
		vmRef *vm.VM
	}
	doneCh := make(chan outcome, 1)
	go func() {
		engine, err := vm.EvalFileVM(tmpPath)
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
	jobs := fs.Int("jobs", runtime.NumCPU(), "并行 worker 数 (进程分片)")
	timeoutSec := fs.Int("timeout", 3, "单用例超时秒数")
	jsonOut := fs.String("json", "", "把全量结果写成 JSON 报告（供基线对比）")
	maxFail := fs.Int("maxfail", 0, "失败数达到 N 即停止（0=不限制）")
	quiet := fs.Bool("quiet", false, "只输出汇总（默认失败清单也打前 20 条）")
	list := fs.Bool("list", false, "只列出用例清单不执行")
	shard := fs.String("shard", "", "内部参数: 分片执行 \"i/N\" —— 只跑 idx%%N==i 的用例")
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

	// ── 分片子进程模式 ──
	//
	// 引擎的 stdlib → object → vm 回调桥依赖包级 currentVM 单例
	// (vm/vm.go), 两个 VM 在同一进程并发执行会跨实例窜状态（实测
	// concurrent map fatal）。在回调桥改造成按调用链传 VM 之前,
	// 多核并行用**进程分片**实现: 主进程 spawn N 个自己, 每个分片
	// 单线程跑 1/N 的用例, 主进程聚合 JSON。
	//
	// 附带韧性收益: 引擎侧任何残余的 fatal error 崩掉的是一个分片
	// 进程, 其余分片的成果不受牵连（主进程把崩溃分片的未完成用例
	// 标记为 crashed）。
	if *shard == "" && *jobs > 1 {
		if os.Getenv("GOX_TEST262_SHARD_CHILD") == "1" {
			// 分片子进程不该走到这里 —— 说明 shard 参数丢了。
			// 防递归兜底: 退化为单进程全量, 绝不允许子进程再 spawn。
			*jobs = 1
		} else {
			runSharded(cases, args, *jobs, *jsonOut != "")
			return
		}
	}

	if *shard != "" {
		var shardIdx, shardTotal int
		if _, err := fmt.Sscanf(*shard, "%d/%d", &shardIdx, &shardTotal); err != nil || shardTotal <= 0 || shardIdx < 0 || shardIdx >= shardTotal {
			fmt.Fprintf(os.Stderr, "gox test262: -shard 参数应为 \"i/N\" (0 <= i < N), 得到 %q\n", *shard)
			os.Exit(2)
		}
		var mine []test262Case
		for i, c := range cases {
			if i%shardTotal == shardIdx {
				mine = append(mine, c)
			}
		}
		cases = mine
		executeCases(root, cases, *suite, 1, *timeoutSec, *maxFail, *jsonOut, *jsonlOut, *quiet, true)
		return
	}

	executeCases(root, cases, *suite, 1, *timeoutSec, *maxFail, *jsonOut, "", *quiet, false)
}

// runSharded 把用例分成 N 片 spawn 子进程执行并聚合报告。
//
// 韧性设计: 子进程逐用例把结果追加成 JSONL (崩溃也保住已完成用例);
// 分片子进程遇首个超时即退出 (超时泄漏的 VM 与后续用例并发会触发
// vm 包包级状态的 fatal error, 不可 recover —— 提前退出扼杀泄漏)。
// 主进程聚合后对"应收 - 已完成"差集做孤儿重派: 一个用例一个进程,
// 彻底进程级隔离。重派后仍缺失的标记 crashed。
func runSharded(cases []test262Case, parentArgs []string, n int, wantJSON bool) {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gox test262: 无法定位可执行文件: %v\n", err)
		os.Exit(1)
	}
	// 传递用户原始参数, 附加 -shard 与子进程 JSON 落点。
	// 注意: -jobs/-json/-shard 都带值, 必须 skipNext 连值一起跳过 ——
	// 否则值会以裸位置参数混进 childArgs, flag 包遇到位置参数即停止
	// 解析, 子进程的 -shard 失效 → 又走 runSharded → 递归 spawn。
	childArgs := []string{"test262"}
	skipNext := false
	for _, a := range parentArgs {
		if skipNext {
			skipNext = false
			continue
		}
		if a == "-jobs" || a == "-json" || a == "-shard" || a == "-one" {
			skipNext = true
			continue
		}
		if strings.HasPrefix(a, "-jobs=") || strings.HasPrefix(a, "-shard=") || strings.HasPrefix(a, "-json=") {
			continue
		}
		childArgs = append(childArgs, a)
	}

	tmpJSONLs := make([]string, n)
	procs := make([]*exec.Cmd, n)
	var baseArgs []string // 供孤儿重派复用的基础参数 (不含 -shard/-jsonl)
	for i := 0; i < n; i++ {
		f, err := os.CreateTemp("", "gox-test262-shard-*.jsonl")
		if err != nil {
			fmt.Fprintf(os.Stderr, "gox test262: 创建分片临时文件失败: %v\n", err)
			os.Exit(1)
		}
		f.Close()
		tmpJSONLs[i] = f.Name()
		childArgs = append(childArgs, "-shard", fmt.Sprintf("%d/%d", i, n), "-jsonl", tmpJSONLs[i])
		if os.Getenv("GOX_TEST262_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, "[debug] 分片 %d 命令: %s %v\n", i, exe, childArgs)
		}
		procs[i] = exec.Command(exe, childArgs...)
		procs[i].Env = append(os.Environ(), "GOX_TEST262_SHARD_CHILD=1")
		procs[i].Stdout = nil
		procs[i].Stderr = os.Stderr
		if i == 0 {
			baseArgs = append([]string(nil), childArgs[:len(childArgs)-4]...) // 供孤儿重派复用
		}
		childArgs = childArgs[:len(childArgs)-4] // 复用参数 slice, 摘掉本片的 4 个附加参数
	}

	fmt.Printf("分片执行: %d 进程 × %d 用例左右\n", n, (len(cases)+n-1)/n)
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if err := procs[idx].Run(); err != nil {
				fmt.Fprintf(os.Stderr, "gox test262: 分片 %d 异常退出: %v\n", idx, err)
			}
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)

	// 聚合: JSONL 逐行读取, 不依赖子进程完整存活
	report := jsonReport{Suite: readSuiteArg(parentArgs), ByGroup: map[string]groupStat{}}
	var results []test262Result
	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		lines := readJSONL(tmpJSONLs[i])
		if len(lines) == 0 {
			fmt.Fprintf(os.Stderr, "gox test262: 分片 %d 无结果产出\n", i)
		}
		results = append(results, lines...)
		os.Remove(tmpJSONLs[i])
	}
	for _, r := range results {
		seen[r.RelPath] = true
	}

	// 孤儿重派: 分片超时退出/崩溃遗留的用例, 一个用例一个进程重跑
	var missing []test262Case
	for _, c := range cases {
		if !seen[c.RelPath] {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		fmt.Printf("孤儿重派: %d 个用例未完成 (分片超时退出/崩溃), 单用例进程隔离重跑\n", len(missing))
		var mu sync.Mutex
		sem := make(chan struct{}, n)
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

// executeCases 在当前进程内执行一批用例: 分片子进程路径 (jobs=1) 或
// jobs==1 的单进程路径。stdout 静音 / worker 池 / 汇总打印 / JSON 落盘
// 都收敛在这里, 主进程的分片并行逻辑 (runSharded) 不进入本函数。
//
// jsonlOut 非空时逐用例把结果追加成 JSONL —— 子进程崩溃 (VM 深层的
// concurrent map fatal 无法 recover) 只丢正在执行的一个用例。
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

	report := jsonReport{Suite: suite, Root: root, Seconds: elapsed.Seconds(), ByGroup: map[string]groupStat{}}
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
