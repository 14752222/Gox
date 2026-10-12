// Package permissions 实现 Gox 的脚本权限模型（v1，Deno 风格）。
//
// 高危能力（fs 写 / process.exit / http 监听 / 定位媒体）在执行前必须持有
// 对应权限。授权只发生在进程启动时（`gox` CLI 解析 --allow-* 标志），
// 运行期不弹窗、不交互式升级——脚本能做什么在启动那一刻就是定死的。
//
// 默认策略：
//   - `gox script.js`（以及 dev/build 产出的运行路径）：最小权限，全部拒绝。
//   - REPL（`gox` 无参数）：交互式会话，默认全部允许（人已坐在终端前）。
//   - 作为 Go 库嵌入（stdlib.SetupGlobals / 测试）：默认全部允许——宿主进程
//     自己就是信任边界，嵌入式场景由宿主自行 Set() 收紧。
//
// 这个包刻意不依赖 stdlib/gfx（它们反过来依赖本包），只依赖 object
// （为了构造错误值）。
package permissions

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/14752222/Gox/object"
)

// Perm 是一类高危能力的权限位。
type Perm uint32

const (
	// FSWrite 覆盖一切会改动文件系统的 fs API（写/追加/建删目录/删文件/改名/复制）。
	FSWrite Perm = 1 << iota
	// ProcessExit 覆盖 process.exit()（脚本可单方面终结宿主进程）。
	ProcessExit
	// NetListen 覆盖 http.createServer(...).listen(...)（打开监听端口）。
	NetListen
	// GeoMedia 覆盖定位（gx/geo）与媒体采集（gx/media 拍照/选图/选视频）。
	GeoMedia

	// All 是全部权限位。新增权限位时必须并入 All。
	All = FSWrite | ProcessExit | NetListen | GeoMedia
)

// spec 把权限位、CLI 标志、用户可读描述绑在一起，是文案的单一来源。
type spec struct {
	perm    Perm
	flag    string
	summary string
}

var specs = []spec{
	{FSWrite, "--allow-fs-write", "允许脚本写入/修改/删除文件系统"},
	{ProcessExit, "--allow-process-exit", "允许脚本调用 process.exit 终结进程"},
	{NetListen, "--allow-net-listen", "允许脚本通过 http.listen 打开监听端口"},
	{GeoMedia, "--allow-geo-media", "允许脚本访问定位（gx/geo）与媒体采集（gx/media）"},
}

// granted 是当前进程已授予的权限位集合。默认 All：嵌入式/测试场景保持
// 既有行为，CLI 在 main() 里显式收紧（见 cmd/gox/main.go）。
var granted atomic.Uint32

func init() {
	granted.Store(uint32(All))
}

// Set 覆盖式设置已授权集合。只在进程启动时调用（CLI / 宿主初始化），
// 运行期不提供"追加授权"——这是刻意的：权限只能收紧在启动那一刻定死。
func Set(mask Perm) {
	granted.Store(uint32(mask))
}

// Granted 返回当前已授权集合（供诊断/测试）。
func Granted() Perm {
	return Perm(granted.Load())
}

// Has 判断某个权限是否已授予。
func Has(p Perm) bool {
	return Perm(granted.Load())&p == p
}

// Require 是能力入口的检查点：未授权时返回 *object.Error（调用方直接
// return 给 VM 抛给脚本），已授权返回 nil。
//
// 错误文案写死格式：`permission denied: <能力> 需要 <标志>（--allow-all 可放行全部）`，
// 让脚本作者一眼知道该加哪个标志。
func Require(p Perm) object.Value {
	if Has(p) {
		return nil
	}
	return object.NewError(fmt.Sprintf("permission denied: %s 需要 %s（--allow-all 可放行全部）", describe(p), flagFor(p)))
}

// ParseFlags 从 CLI 参数中剥离 --allow-* 权限标志，返回剩余参数与授权集合。
// 未知权限标志报错（宁可失败也不静默忽略——安全相关的 flag 打错字必须被发现）。
// 特殊值 --allow-all / -A 授予全部。
func ParseFlags(args []string) ([]string, Perm, error) {
	mask := Perm(0)
	var rest []string
	for _, a := range args {
		if a == "--allow-all" || a == "-A" {
			mask |= All
			continue
		}
		if strings.HasPrefix(a, "--allow-") {
			matched := false
			for _, s := range specs {
				if a == s.flag {
					mask |= s.perm
					matched = true
					break
				}
			}
			if !matched {
				return nil, 0, fmt.Errorf("未知权限标志 %s（可用：%s, --allow-all）", a, FlagList())
			}
			continue
		}
		rest = append(rest, a)
	}
	return rest, mask, nil
}

// FlagList 返回可用权限标志的逗号分隔列表（帮助文案与报错共用）。
func FlagList() string {
	names := make([]string, len(specs))
	for i, s := range specs {
		names[i] = s.flag
	}
	return strings.Join(names, ", ")
}

// HelpLines 返回每个标志的说明行（gox help 用）。
func HelpLines() []string {
	lines := make([]string, 0, len(specs)+1)
	for _, s := range specs {
		lines = append(lines, fmt.Sprintf("  %-22s %s", s.flag, s.summary))
	}
	lines = append(lines, fmt.Sprintf("  %-22s %s", "--allow-all (-A)", "授予以上全部权限"))
	return lines
}

func flagFor(p Perm) string {
	for _, s := range specs {
		if s.perm == p {
			return s.flag
		}
	}
	return "--allow-all"
}

func describe(p Perm) string {
	switch p {
	case FSWrite:
		return "文件系统写入"
	case ProcessExit:
		return "process.exit"
	case NetListen:
		return "http 端口监听"
	case GeoMedia:
		return "定位/媒体采集"
	}
	return "高危能力"
}
