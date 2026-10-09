package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

// ===== 构建产物来源元数据 (rFf4lR) =====
//
// Go 默认 `-buildvcs=true` 已经把来源元数据嵌进二进制（`go version -m <bin>`
// 能看到 vcs.revision / vcs.time / vcs.modified），所以缺口从来不是"二进制
// 没有元数据"，而是**没人把它交到使用者手上**：
//
//	① `gox version` 只报手写常量，人拿不到 commit;
//	② test262 结果 JSON 里**完全不记录**产出它的引擎身份 —— 账本一旦离开
//	   产出它的二进制就永久失去身份。历史两次 A/B 翻车（base-b381cbc.json /
//	   real-base.json 的内容与文件名声称的提交不符，以及我这次拿 ryB64Z 之前
//	   编译的缓存二进制当 rj9MwH 的 base）都是这个洞造成的。
//
// 本文件把身份读出来 (debug.ReadBuildInfo) 并写进两个出口: `gox version` 与
// test262 的 jsonReport.engine。刻意**不引入 ldflags 注入**: Go 自带的 vcs
// stamping 已经是唯一真相来源，再注入一份就是第二个真相。
//
// 注意 `vcs.modified=true` (脏树) 必须显式报出来 —— 脏树产出的二进制不能当
// A/B 基准, 也不能进 release。

// engineInfo 是一份"产出这个结果的引擎身份"。
//
// Revision 为空 (VcsPresent=false) 表示这次构建**没有** vcs stamping —— 例如
// 在仓库外构建、显式 `-buildvcs=false`、或源码以 tarball 形式分发。这种
// 二进制产出的 test262 JSON 不可当基准, 必须显式标出来而不是留空字符串
// 让人误读成"干净"。
type engineInfo struct {
	Version    string `json:"version"`      // gox 版本号 (main.version)
	Revision   string `json:"vcs_revision"` // 提交号; 空 = 无 vcs stamping
	Modified   bool   `json:"vcs_modified"` // 工作树是否脏
	VcsTime    string `json:"vcs_time"`     // 提交时间 (RFC3339)
	VcsPresent bool   `json:"vcs_present"`  // 是否有 vcs stamping
	GOOS       string `json:"goos"`
	GOARCH     string `json:"goarch"`
	GoVersion  string `json:"go_version"`
	BuiltAt    string `json:"built_at"` // 可执行文件 mtime (构建时刻的近似)
}

// currentEngineInfo 读出当前二进制的来源元数据。
func currentEngineInfo() engineInfo {
	info := engineInfo{
		Version:    version,
		Revision:   "",
		Modified:   false,
		VcsPresent: false,
		GOOS:       runtime.GOOS,
		GOARCH:     runtime.GOARCH,
		GoVersion:  runtime.Version(),
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		info.Revision, info.Modified, info.VcsTime = parseVCSSettings(bi.Settings)
		if bi.GoVersion != "" {
			// 构建该二进制所用的 toolchain, 比 runtime.Version() 更贴切
			// (交叉编译/多 toolchain 环境下二者可能不同)。
			info.GoVersion = bi.GoVersion
		}
	}
	info.VcsPresent = info.Revision != ""
	info.BuiltAt = binaryMtime()
	return info
}

// parseVCSSettings 从 buildinfo 的 Settings 里取出 vcs 三元组。
//
// Go 只在**工作树脏**时写 vcs.modified, 且值是字符串 "true"/"false";
// 干净树干脆不写这一项 —— 所以"键不存在"与"值为 false"都要按干净处理。
func parseVCSSettings(settings []debug.BuildSetting) (revision string, modified bool, vcsTime string) {
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			revision = strings.TrimSpace(s.Value)
		case "vcs.modified":
			modified = strings.EqualFold(strings.TrimSpace(s.Value), "true")
		case "vcs.time":
			vcsTime = strings.TrimSpace(s.Value)
		}
	}
	return
}

// binaryMtime 返回当前可执行文件的修改时间 (RFC3339)。
//
// debug.BuildInfo 里没有"构建时刻"这一项 (只有 vcs.time = 提交时刻), 故用
// 可执行文件自身的 mtime 作近似 —— 它足以区分"同一 commit 的不同次构建"。
// 取不到 (例如被替换/无权限) 时留空, 不伪造。
func binaryMtime() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	st, err := os.Stat(exe)
	if err != nil {
		return ""
	}
	return st.ModTime().UTC().Format(time.RFC3339)
}

// dirtyLabel 给出供人类读的脏树标记。
func (e engineInfo) dirtyLabel() string {
	if e.Modified {
		return " (dirty)"
	}
	return ""
}

// String 是 `gox version` 之外的多行身份摘要 (test262 打印头用)。
func (e engineInfo) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "gox %s", e.Version)
	if e.VcsPresent {
		fmt.Fprintf(&b, "  commit %s%s", shortRev(e.Revision), e.dirtyLabel())
	} else {
		b.WriteString("  commit <无 vcs 元数据: 不可作 A/B 基准>")
	}
	fmt.Fprintf(&b, "  %s/%s  %s", e.GOOS, e.GOARCH, e.GoVersion)
	if e.BuiltAt != "" {
		fmt.Fprintf(&b, "  built %s", e.BuiltAt)
	}
	return b.String()
}

// shortRev 把完整提交号缩成 12 位 (与 git log --abbrev 的直觉一致)。
func shortRev(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

// newJSONReport 是 jsonReport 的**唯一**构造入口。
//
// 刻意收口成构造函数而不是让两处各自写字面量: 分片聚合与单进程顺序是两条
// 独立的落盘路径, 只要有一处忘了 Engine, 那条路径产出的账本就永久失去身份
// —— 而"那份 JSON 少了 engine 段"要等某次 A/B 翻车才会被发现 (反向验证 D
// 组实测: 字面量写法下, 单测照样全绿)。构造函数让"忘记"变成编译错误。
func newJSONReport(suite, root string) jsonReport {
	return jsonReport{Engine: currentEngineInfo(), Suite: suite, Root: root, ByGroup: map[string]groupStat{}}
}

// printEngineBanner 是 test262 汇总头里的引擎身份行。
//
// 引擎身份必须出现在**人读的汇总里**, 不能只在 -json 里: 一次跑完的结论往往
// 是以"终端上这行数字"的形式进台账/评论的, 而那行数字一旦离开终端就再也
// 说不清是哪个 commit 跑出来的 (rFf4lR)。身份缺失/脏树时额外告警 —— 这两种
// 情况下的数字都不该被当成 A/B 基准。
func printEngineBanner(e engineInfo) {
	fmt.Printf("引擎: %s\n", e.String())
	switch {
	case !e.VcsPresent:
		fmt.Printf("  ⚠ 本二进制无 vcs 元数据, 结果不可作 A/B 基准\n")
	case e.Modified:
		fmt.Printf("  ⚠ 工作树是脏的, 结果不可作 A/B 基准\n")
	}
}

// printVersion 是 `gox version` 的输出。
//
// 除了手写常量, 还要把 commit / 脏树 / 平台 / 构建时刻一并打印 —— 目标是
// **一次输出能唯一确定一棵工作树**。缺 vcs 元数据时明确写出来, 而不是安静
// 地少打一行。
//
// 拆出 printVersionTo 是为了让"输出面"可被单测钉住: 只测结构体字段的话,
// 退化成"少打几行"照样全绿 (反向验证 C 组实测过)。
func printVersion() { printVersionTo(os.Stdout, currentEngineInfo()) }

func printVersionTo(w io.Writer, e engineInfo) {
	fmt.Fprintf(w, "gox %s\n", e.Version)
	if e.VcsPresent {
		fmt.Fprintf(w, "commit  %s%s\n", e.Revision, e.dirtyLabel())
		if e.VcsTime != "" {
			fmt.Fprintf(w, "commit-time %s\n", e.VcsTime)
		}
	} else {
		fmt.Fprintf(w, "commit  <无 vcs 元数据: 该二进制不可作 A/B 基准>\n")
	}
	fmt.Fprintf(w, "built   %s\n", e.BuiltAt)
	fmt.Fprintf(w, "toolchain %s %s/%s\n", e.GoVersion, e.GOOS, e.GOARCH)
}
