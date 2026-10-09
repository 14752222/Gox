package main

import (
	"encoding/json"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

// ===== 构建产物来源元数据 (rFf4lR) =====
//
// 目标: 一份 test262 JSON 能唯一定位产出它的 commit; 用脏树跑一次, JSON 必须
// 标 vcs_modified=true; `gox version` 的输出能唯一确定一棵工作树。
//
// 单测层面钉住三件事: vcs 三元组的解析口径、engine 段的 JSON 键面、以及
// "没有 vcs stamping" 时**显式标记**而不是留空字符串让人误读成干净。

// TestParseVCSSettings 钉住解析口径:
//   - 干净树**不写** vcs.modified (键不存在 ⇒ false);
//   - 脏树写 vcs.modified=true (字符串 "true"/"false" 都认);
//   - 三者互不影响, 键序无关。
func TestParseVCSSettings(t *testing.T) {
	cases := []struct {
		name         string
		settings     []debug.BuildSetting
		wantRev      string
		wantModified bool
		wantTime     string
	}{
		{
			name: "干净树: 只有 revision 与 time, 没有 modified 键",
			settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "8db9b1d0c0ffee1234567890abcdef0123456789"},
				{Key: "vcs.time", Value: "2026-10-09T08:29:47Z"},
				{Key: "-trimpath", Value: "true"},
			},
			wantRev:      "8db9b1d0c0ffee1234567890abcdef0123456789",
			wantModified: false,
			wantTime:     "2026-10-09T08:29:47Z",
		},
		{
			name: "脏树: modified=true 必须被识别",
			settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "deadbeef"},
				{Key: "vcs.modified", Value: "true"},
				{Key: "vcs.time", Value: "2026-10-09T09:00:00Z"},
			},
			wantRev:      "deadbeef",
			wantModified: true,
			wantTime:     "2026-10-09T09:00:00Z",
		},
		{
			name: "modified=false 显式写死也算干净",
			settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "abc"},
				{Key: "vcs.modified", Value: "false"},
			},
			wantRev:      "abc",
			wantModified: false,
		},
		{
			name:         "无 vcs stamping: 全空",
			wantRev:      "",
			wantModified: false,
		},
		{
			name: "值带空白也要认 (Go 的 vcs.time 无空白, 但别被空白骗过)",
			settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "  cafebabe  "},
				{Key: "vcs.modified", Value: " TRUE "},
			},
			wantRev:      "cafebabe",
			wantModified: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rev, mod, vt := parseVCSSettings(tc.settings)
			if rev != tc.wantRev || mod != tc.wantModified || vt != tc.wantTime {
				t.Fatalf("parseVCSSettings = (%q,%v,%q), 期望 (%q,%v,%q)",
					rev, mod, vt, tc.wantRev, tc.wantModified, tc.wantTime)
			}
		})
	}
}

// TestCurrentEngineInfoHasPlatformIdentity 平台/版本/号三项永远非空 —— 它们是
// "唯一定位产物" 的最小面, 即使 vcs stamping 缺失也必须能区分平台与 toolchain。
func TestCurrentEngineInfoHasPlatformIdentity(t *testing.T) {
	e := currentEngineInfo()
	if e.Version == "" {
		t.Fatal("engine version 不该为空")
	}
	if e.GOOS != runtime.GOOS || e.GOARCH != runtime.GOARCH {
		t.Fatalf("平台不符: %s/%s vs %s/%s", e.GOOS, e.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
	if e.GoVersion == "" {
		t.Fatal("go_version 不该为空")
	}
	// VcsPresent 必须与 Revision 同时翻转: 有 revision 才算"有身份"。
	if e.VcsPresent != (e.Revision != "") {
		t.Fatalf("VcsPresent=%v 与 Revision=%q 不自洽", e.VcsPresent, e.Revision)
	}
}

// TestEngineInfoJSONCarriesIdentity engine 段必须带全部身份键 —— 少一个键,
// 账本就少一维可比对信息 (历史教训: 只有 suite/root/total 时, 两份 JSON
// 长得一样却来自不同 commit)。
func TestEngineInfoJSONCarriesIdentity(t *testing.T) {
	e := engineInfo{
		Version: "0.9.0", Revision: "abc123def456", Modified: true,
		VcsTime: "2026-10-09T08:00:00Z", VcsPresent: true,
		GOOS: "linux", GOARCH: "amd64", GoVersion: "go1.26.2", BuiltAt: "2026-10-09T09:00:00Z",
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{
		`"version"`, `"vcs_revision"`, `"vcs_modified"`, `"vcs_time"`,
		`"vcs_present"`, `"goos"`, `"goarch"`, `"go_version"`, `"built_at"`,
	} {
		if !strings.Contains(string(data), key) {
			t.Fatalf("engine JSON 缺键 %s: %s", key, data)
		}
	}
	// 脏树必须序列化成 true, 不能因为 bool 零值被省略而看不出来。
	if !strings.Contains(string(data), `"vcs_modified":true`) {
		t.Fatalf("脏树标记没落进 JSON: %s", data)
	}
}

// TestJSONReportAlwaysCarriesEngine 两条落盘路径 (并发分片聚合 / 单进程顺序跑)
// 都必须带上 engine 段, 否则某条路径产出的账本仍然没有身份。
func TestJSONReportAlwaysCarriesEngine(t *testing.T) {
	eng := currentEngineInfo()
	for name, rep := range map[string]jsonReport{
		"聚合路径": {Engine: eng, Suite: "language"},
		"顺序路径": {Engine: eng, Suite: "built-ins", Root: "/tmp/test262"},
	} {
		data, err := json.Marshal(rep)
		if err != nil {
			t.Fatalf("%s marshal: %v", name, err)
		}
		var back map[string]json.RawMessage
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatalf("%s unmarshal: %v", name, err)
		}
		raw, ok := back["engine"]
		if !ok {
			t.Fatalf("%s 的 JSON 缺 engine 段: %s", name, data)
		}
		var e engineInfo
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatalf("%s engine 段解析失败: %v", name, err)
		}
		if e.GOOS != runtime.GOOS {
			t.Fatalf("%s engine.goos = %q, 期望 %q", name, e.GOOS, runtime.GOOS)
		}
	}
}

// TestShortRev 短提交号别越界, 也别把短串截坏。
func TestShortRev(t *testing.T) {
	cases := map[string]string{
		"8db9b1d0c0ffee1234567890abcdef0123456789": "8db9b1d0c0ff",
		"abcdef": "abcdef",
		"":       "",
	}
	for in, want := range cases {
		if got := shortRev(in); got != want {
			t.Fatalf("shortRev(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

// TestEngineInfoStringFlagsMissingVCS 无 vcs stamping 时, 摘要里必须**写出来**
// 而不是安静地少一段 —— 否则读者会把"没打印 commit"读成"commit 干净"。
func TestEngineInfoStringFlagsMissingVCS(t *testing.T) {
	clean := engineInfo{Version: "0.9.0", Revision: "abc123", VcsPresent: true, GOOS: "linux", GOARCH: "amd64", GoVersion: "go1.26.2"}
	if s := clean.String(); !strings.Contains(s, "abc123") || strings.Contains(s, "无 vcs") {
		t.Fatalf("干净树的摘要不对: %s", s)
	}
	dirty := clean
	dirty.Modified = true
	if s := dirty.String(); !strings.Contains(s, "dirty") {
		t.Fatalf("脏树摘要缺 dirty 标记: %s", s)
	}
	noVCS := engineInfo{Version: "0.9.0", GOOS: "linux", GOARCH: "amd64", GoVersion: "go1.26.2"}
	if s := noVCS.String(); !strings.Contains(s, "无 vcs") {
		t.Fatalf("无 vcs 时摘要必须显式告警: %s", s)
	}
}
