package hostbridge

// ===== 三端宿主契约测试 (桌面可跑) =====
//
// ## 为什么在这里测, 而不是在真机上
//
// 三端的 NativeHost 实现住在 package main 里, 真机跑 Go test 需要各自的
// instrumented runner / hdc 推包 —— 那是真机工程。但宿主契约真正会算错的
// 部分**不在**跨语言那一跳里, 而在「声明了什么」与「实现了什么」这两张表上:
// 它们是纯数据, 而且就写在平台源码里。
//
// 所以这里沿用 gfx/mobile/fold.go 的手法: **从平台源码把这两张表解析出来**,
// 装进本包的桥接宿主, 然后喂给 gfx/hosttest 跑全套平台无关断言。
//
// 这么做比在真机上跑还强两处:
//  1. 桌面 `go test` 就能跑, 不需要模拟器/设备;
//  2. 清单改了测试自动跟上 —— 解析的是**真实源文件**, 不是在这里维护的副本
//     (副本会漂移, 而漂移的断言比没有断言更坏: 它绿着, 但什么也没守住)。
//
// ## 抓的违规
//
//   - gfx/hosttest 的全套契约断言 (声明形状 / 声明稳定 / 纯上报型 / 声明即可用 /
//     canIUse 一致性);
//   - **少报**: dispatch 里实现了、capabilities 里却没声明的方法 —— canIUse 说
//     "没有", 白实现。这条黑盒测不出来 (契约无法枚举宿主没声明的东西), 只能靠
//     这两张表的交叉比对。

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/14752222/Gox/gfx/hosttest"
)

// repoRoot 是仓库根 (本包在 gfx/hostbridge/)。
var repoRoot = filepath.Join("..", "..")

// platformSpec 描述一端的宿主源码: 声明表与实现表各在哪个文件的哪个锚点后。
type platformSpec struct {
	name string
	// caps 锚点: 声明表 (capabilities 返回的那张能力清单)
	capsFile string
	capsRe   *regexp.Regexp
	// impl 锚点: dispatch 分支 (真正实现了哪些 Call 方法)
	implFile string
	implRe   *regexp.Regexp
}

var platforms = []platformSpec{
	{
		name:     "android",
		capsFile: "app/android/app/src/main/kotlin/com/gox/GoxNativeHost.kt",
		capsRe:   regexp.MustCompile(`CAPABILITY_LIST\s*=\s*listOf\(`),
		implFile: "app/android/app/src/main/kotlin/com/gox/GoxNativeHostImpl.kt",
		implRe:   regexp.MustCompile(`"([a-z][a-zA-Z0-9]*\.[a-zA-Z0-9]+)"\s*->`),
	},
	{
		name:     "ios",
		capsFile: "app/ios/App/Sources/NativeHost.swift",
		capsRe:   regexp.MustCompile(`capabilityList:\s*\[String\]\s*=\s*\[`),
		implFile: "app/ios/App/Sources/NativeHost.swift",
		implRe:   regexp.MustCompile(`case\s+"([a-z][a-zA-Z0-9]*\.[a-zA-Z0-9]+)"\s*:`),
	},
	{
		name:     "harmony",
		capsFile: "app/harmony/entry/src/main/ets/native/GoxNativeHostImpl.ets",
		// 锚点必须落在数组字面量上: 若停在 `capabilities(): string {`, 括号配平
		// 会在类型标注 `string[]` 处就闭合, 截出一个空块 (解析 0 项, 断言假绿)。
		capsRe:   regexp.MustCompile(`const\s+caps:\s*string\[\]\s*=\s*\[`),
		implFile: "app/harmony/entry/src/main/ets/native/GoxNativeHostImpl.ets",
		// 鸿蒙的 case 走 Methods.X 常量 (见同目录 NativeHost.ets), 单独解析。
		implRe: regexp.MustCompile(`case\s+Methods\.([A-Z0-9_]+)\s*:`),
	},
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("读平台源文件 %s 失败: %v —— 契约测试依赖解析真实宿主源码; 文件被移动/重命名时请同步 platforms 表", rel, err)
	}
	return string(b)
}

// stripLineComments 去掉整行注释 —— 三端清单里都夹着 "// 纯上报型" 之类的注释,
// 不剔掉会把注释里的词当成能力名。方法名里不会出现 "//", 逐行判断是安全的。
func stripLineComments(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// bracketBlock 从 anchor 匹配位置之后截取到括号配平为止 (支持 () 与 [])。
//
// 为什么不用"到下一个空行": 三端清单的换行风格不一致 (Android 一项一行,
// iOS 一行三项)。按括号配平截取对两种都成立。
func bracketBlock(src string, anchor *regexp.Regexp) (string, bool) {
	loc := anchor.FindStringIndex(src)
	if loc == nil {
		return "", false
	}
	start := strings.IndexAny(src[loc[1]-1:], "([")
	if start < 0 {
		return "", false
	}
	open := src[loc[1]-1:][start]
	var close byte = ')'
	if open == '[' {
		close = ']'
	}
	depth := 0
	for i := loc[1] - 1 + start; i < len(src); i++ {
		switch src[i] {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return src[loc[1]+start : i+1], true
			}
		}
	}
	return "", false
}

var (
	reQuotedDouble = regexp.MustCompile(`"([^"]+)"`)
	reQuotedSingle = regexp.MustCompile(`'([^']+)'`)
	reMethodsRef   = regexp.MustCompile(`Methods\.([A-Z0-9_]+)`)
)

// harmonyMethods 是鸿蒙 Methods 常量表 (NativeHost.ets) 的解析结果: 常量名 → 方法名。
func harmonyMethods(t *testing.T) map[string]string {
	t.Helper()
	src := readRepoFile(t, "app/harmony/entry/src/main/ets/native/NativeHost.ets")
	out := map[string]string{}
	for _, m := range regexp.MustCompile(`static readonly ([A-Z0-9_]+): string = '([^']+)'`).FindAllStringSubmatch(src, -1) {
		out[m[1]] = m[2]
	}
	if len(out) == 0 {
		t.Fatalf("解析鸿蒙 Methods 常量表得到 0 项: 契约测试的「已实现方法」集合会是空的, 少报检测将假绿")
	}
	return out
}

// declaredCaps 解析一端宿主声明的能力表。
func (p platformSpec) declaredCaps(t *testing.T) []string {
	t.Helper()
	src := stripLineComments(readRepoFile(t, p.capsFile))
	block, ok := bracketBlock(src, p.capsRe)
	if !ok {
		t.Fatalf("%s: 在 %s 里没定位到声明表 (锚点 %v) —— 宿主源码结构变了, 请同步 platforms 表", p.name, p.capsFile, p.capsRe)
	}
	var out []string
	for _, m := range reQuotedDouble.FindAllStringSubmatch(block, -1) {
		out = append(out, m[1])
	}
	for _, m := range reQuotedSingle.FindAllStringSubmatch(block, -1) {
		out = append(out, m[1])
	}
	// 鸿蒙的清单是「裸的 Methods.X 常量 + 引号能力名」混写 (没有引号包住常量),
	// 所以要在块上再扫一遍常量引用, 与上面收集到的引号项取并集。
	if p.name == "harmony" {
		mt := harmonyMethods(t)
		for _, m := range reMethodsRef.FindAllStringSubmatch(block, -1) {
			v, ok := mt[m[1]]
			if !ok {
				t.Fatalf("%s: 声明表里出现未知的 Methods.%s (常量表在 NativeHost.ets)", p.name, m[1])
			}
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s: 声明表解析出 0 项 —— 解析失败会让下面所有断言假绿, 必须查", p.name)
	}
	return out
}

// dispatchTable 是一端 dispatch 的解析结果。
type dispatchTable struct {
	// implemented 是真正实现了的方法 (能给出结果或 Pending)。
	implemented []string
	// degraded 是**显式降级**的方法: case 体直接返回 ERR_UNSUPPORTED。
	//
	// 它们存在的意义是给一条可读的错误信息 ("iOS 不允许应用自杀"), 而不是"支持"。
	// 因此它们**不该**出现在声明表里 —— 声明了 canIUse 就说谎; 也不该被当成
	// "实现了却没声明"的少报。两边都不算, 单独列出来。
	degraded []string
}

// reUnsupported 匹配三端各自的"显式降级"标记 (常量名一致, 故一条通吃)。
var reUnsupported = regexp.MustCompile(`ERR_UNSUPPORTED`)

// parseDispatch 解析一端宿主的 dispatch 分支, 区分真实现与显式降级。
func (p platformSpec) parseDispatch(t *testing.T) dispatchTable {
	t.Helper()
	src := stripLineComments(readRepoFile(t, p.implFile))
	type hit struct {
		method string
		end    int // case 头部结束位置, 块从这里开始
	}
	var hits []hit
	mt := map[string]string{}
	if p.name == "harmony" {
		mt = harmonyMethods(t)
	}
	for _, idx := range p.implRe.FindAllStringSubmatchIndex(src, -1) {
		// 子组 1 是方法名 (或鸿蒙的常量名)。
		name := src[idx[2]:idx[3]]
		if p.name == "harmony" {
			v, ok := mt[name]
			if !ok {
				t.Fatalf("%s: dispatch 里出现未知的 Methods.%s", p.name, name)
			}
			name = v
		}
		hits = append(hits, hit{method: name, end: idx[1]})
	}
	if len(hits) == 0 {
		t.Fatalf("%s: dispatch 解析出 0 个方法 —— 少报检测会假绿, 必须查", p.name)
	}
	var out dispatchTable
	for i, h := range hits {
		stop := len(src)
		if i+1 < len(hits) {
			stop = hits[i+1].end
		}
		body := src[h.end:stop]
		// 块尾要截在 default/else 之前, 否则 default 的 ERR_UNSUPPORTED 会污染
		// 最后一个 case 的判断。
		if cut := strings.Index(body, "default:"); cut >= 0 {
			body = body[:cut]
		}
		if cut := strings.Index(body, "else ->"); cut >= 0 {
			body = body[:cut]
		}
		if reUnsupported.MatchString(body) {
			out.degraded = append(out.degraded, h.method)
		} else {
			out.implemented = append(out.implemented, h.method)
		}
	}
	return out
}

// fakeLanguageSide 造一个"语言侧"应答器: 模拟 Kotlin / Swift / ArkTS 那一半。
//
// 三档应答, 与真实宿主一一对应:
//   - 实现了的方法 → Pending (受理, 稍后回填)。合法应答, 且不触发副作用;
//   - 显式降级的方法 → unsupported 信封。**必须**如实降级: 若这里也返回 Pending,
//     契约的「声明即可用」断言就被架空了 —— iOS 的 app.exit (声明了却因平台限制
//     只能 unsupported) 正是这一类, 靠这一档才抓得出来;
//   - 其余 → error, 桥接宿主同样译成 unsupported。
func fakeLanguageSide(impl map[string]bool, degraded map[string]bool) CallFn {
	return func(method, argsJSON string) (string, error) {
		switch {
		case degraded[method]:
			return `{"__errCode":"unsupported","__errMsg":"宿主显式降级"}`, nil
		case impl[method]:
			return PendingMarker, nil
		}
		return "", errors.New("语言侧未实现")
	}
}

func toSet(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// TestPlatformHostContract 对三端宿主各跑一次平台无关契约套件。
func TestPlatformHostContract(t *testing.T) {
	for _, p := range platforms {
		t.Run(p.name, func(t *testing.T) {
			caps := p.declaredCaps(t)
			d := p.parseDispatch(t)
			host := New(
				func() (string, error) { return strings.Join(caps, ","), nil },
				fakeLanguageSide(toSet(d.implemented), toSet(d.degraded)),
				"%s 宿主未实现该方法",
			)
			hosttest.RunNativeHostContract(t, host)
		})
	}
}

// TestPlatformNoUnderreport 抓「少报」: dispatch 实现了却没声明。
//
// hosttest 的黑盒断言抓不到这一侧 (它无法枚举宿主没声明的东西), 所以在这里用
// 两张表的交叉比对补上 —— 这正是验收里「故意少报一个时测试必须失败」那一条。
func TestPlatformNoUnderreport(t *testing.T) {
	for _, p := range platforms {
		t.Run(p.name, func(t *testing.T) {
			declared := toSet(p.declaredCaps(t))
			d := p.parseDispatch(t)
			var missing []string
			for _, m := range d.implemented {
				if !declared[m] {
					missing = append(missing, m)
				}
			}
			if len(missing) > 0 {
				t.Errorf("%s 宿主实现了却没声明: %v —— canIUse 会对这些方法说 false (脚本侧当它不存在), 要么补进声明表, 要么把实现删掉", p.name, missing)
			}
			// 反向: 显式降级的方法**不该**被声明 (声明了 canIUse 就说谎)。
			// 这条与契约套件的「声明即可用」同源, 但那边的假语言侧若不降级就抓不到,
			// 这里用解析结果直接钉住, 不依赖套件。
			var lied []string
			for _, m := range d.degraded {
				if declared[m] {
					lied = append(lied, m)
				}
			}
			if len(lied) > 0 {
				t.Errorf("%s 宿主声明了显式降级的方法: %v —— 这些方法的实现体直接返回 unsupported, 声明它们会让 canIUse 说 true 而调用必失败; 从声明表删掉 (canIUse 诚实回答 false, 脚本自然不会调)", p.name, lied)
			}
		})
	}
}

// ── 负例自测: 证明少报检测真的会红 ──
//
// 绿着的断言不等于有效的断言。

// TestUnderreportDetectionIsReal 证明少报检测不是空转: 抽掉一个已实现方法后必须报错。
func TestUnderreportDetectionIsReal(t *testing.T) {
	p := platforms[0] // android
	impl := p.parseDispatch(t).implemented
	if len(impl) == 0 {
		t.Fatal("解析不到实现表, 负例无从构造")
	}
	// 声明表里抽掉第一项已实现方法 —— 模拟"宿主忘了报"。
	declared := toSet(p.declaredCaps(t))
	victim := impl[0]
	delete(declared, victim)

	var missing []string
	for _, m := range impl {
		if !declared[m] {
			missing = append(missing, m)
		}
	}
	if len(missing) == 0 || missing[0] != victim {
		t.Fatalf("少报检测没抓到被抽掉的 %q: 断言是空转的", victim)
	}
}

// TestBridgePassesCapabilitiesThrough 证明桥接没把清单"加工"掉 —— 套件看到的就是
// 宿主声明的原文。
//
// 这条是 TestPlatformHostContract 的价值担保: 它绿, 必须是因为三端清单真的合规,
// 而不是因为桥接在中间把违规项滤掉了。套件自己的负例自测 (证明它对违规清单
// 确实会红) 在 gfx/hosttest/hosttest_test.go 里, 这里不重复。
func TestBridgePassesCapabilitiesThrough(t *testing.T) {
	raw := "device.info, battery ,  ,\tnetwork\n"
	host := New(func() (string, error) { return raw, nil }, fakeLanguageSide(nil, nil), "%s")
	got := host.Capabilities()
	// 除了 Trim + 丢空 (内核注册时的同款处理), 一个字都不该变 —— 尤其不该偷偷
	// 滤掉 battery / network 这类违反契约的项。
	want := []string{"device.info", "battery", "network"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("桥接改动了能力清单: 得到 %v, 期望 %v", got, want)
	}
}
