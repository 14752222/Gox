package update

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// fakeBin 生成一段确定性内容 (长度 n), 作为"二进制"的替身。
func fakeBin(seed byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = seed + byte(i%251)
	}
	return b
}

func sha256hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// serveBinary 起一个只服务一段字节的静态服务器, 并记录收到的 Range 头。
func serveBinary(t *testing.T, content []byte, ranges *atomic.Int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ranges != nil {
			if r.Header.Get("Range") != "" {
				ranges.Add(1)
			}
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(content)))
		w.Write(content)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// manifestJSON 把一组 release 编码成清单文本。
func manifestJSON(t *testing.T, rels ...Release) string {
	t.Helper()
	b, err := json.Marshal(Manifest{Releases: rels})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func serveManifest(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ===== PickRelease: 通道与 semver =====

func rel(v, ch string) Release {
	return Release{Version: v, Channel: ch, Files: map[string]FileEntry{"linux/amd64": {URL: "https://x/y"}}}
}

func TestPickReleaseChannelAndSemver(t *testing.T) {
	cases := []struct {
		name    string
		rels    []Release
		current string
		pre     bool
		want    string // 期望选中的版本; "" 表示"已是最新"; "ERR" 表示无可用版本
	}{
		{"stable 通道忽略 pre", []Release{rel("1.0.0", "stable"), rel("2.0.0-pre.1", "pre")}, "1.0.0", false, ""},
		{"开启 pre 取最大", []Release{rel("1.0.0", "stable"), rel("2.0.0-pre.1", "pre")}, "1.0.0", true, "2.0.0-pre.1"},
		{"pre 小于 stable 不选", []Release{rel("2.0.0", "stable"), rel("2.0.0-pre.1", "pre")}, "1.0.0", true, "2.0.0"},
		{"同号 stable 压过 pre", []Release{rel("2.0.0", "pre"), rel("2.0.0", "stable")}, "1.0.0", true, "2.0.0"},
		{"pre.2 > pre.1", []Release{rel("2.0.0-pre.1", "pre"), rel("2.0.0-pre.2", "pre")}, "1.9.0", true, "2.0.0-pre.2"},
		{"pre.10 > pre.9 (数值比较)", []Release{rel("2.0.0-pre.9", "pre"), rel("2.0.0-pre.10", "pre")}, "1.9.0", true, "2.0.0-pre.10"},
		{"v 前缀宽容", []Release{rel("v2.0.0", "stable")}, "1.0.0", false, "v2.0.0"},
		{"省略 channel 视为 stable", []Release{rel("2.0.0", "")}, "1.0.0", false, "2.0.0"},
		{"脏版本号跳过不致命", []Release{rel("not-a-version", "stable"), rel("1.5.0", "stable")}, "1.0.0", false, "1.5.0"},
		{"current 解析失败视为要更新", []Release{rel("0.0.1", "stable")}, "dev-build", false, "0.0.1"},
		{"清单为空报错", nil, "0.0.1", false, "ERR"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := PickRelease(&Manifest{Releases: c.rels}, c.current, CheckOptions{PreRelease: c.pre})
			switch c.want {
			case "ERR":
				if err == nil {
					t.Fatalf("期望报错, 实际选中 %+v", got)
				}
			case "":
				if got != nil || err != nil {
					t.Fatalf("期望已是最新 (nil,nil), 实际 (%v,%v)", got, err)
				}
			default:
				if err != nil || got == nil || got.Version != c.want {
					t.Fatalf("期望 %s, 实际 (%v,%v)", c.want, got, err)
				}
			}
		})
	}
}

// ===== Download: 流式 / 校验 / 断点续传 =====

func TestDownloadStreamVerifyAndRename(t *testing.T) {
	content := fakeBin(1, 300*1024) // 300KB, 越过几轮 64KB 缓冲
	srv := serveBinary(t, content, nil)
	rel := Release{Version: "1.0.0", Files: map[string]FileEntry{
		PlatformKey(): {URL: srv.URL, SHA256: sha256hex(content), Size: int64(len(content))},
	}}
	dir := t.TempDir()
	dest := filepath.Join(dir, "app.bin")

	var calls int
	var lastDone, lastTotal int64
	err := Download(&rel, dest, DownloadOptions{Progress: func(done, total int64) {
		calls++
		if done < lastDone {
			t.Fatalf("进度回退: %d < %d", done, lastDone)
		}
		lastDone, lastTotal = done, total
	}})
	if err != nil {
		t.Fatal(err)
	}
	if calls == 0 || lastDone != int64(len(content)) || lastTotal != int64(len(content)) {
		t.Fatalf("进度回调异常: calls=%d done=%d total=%d", calls, lastDone, lastTotal)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatal("下载内容与源不符")
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatal(".part 应已被改名为最终文件")
	}
}

func TestDownloadResumeAfterInterrupt(t *testing.T) {
	content := fakeBin(2, 200*1024)
	var rangeHits atomic.Int64
	srv := serveBinary(t, content, &rangeHits)
	rel := Release{Version: "1.0.0", Files: map[string]FileEntry{
		PlatformKey(): {URL: srv.URL, SHA256: sha256hex(content)},
	}}
	dir := t.TempDir()
	dest := filepath.Join(dir, "app.bin")
	part := dest + ".part"

	// 模拟"上次下到一半进程被杀": 预置半截 .part (内容取前 50KB 的真前缀,
	// 这样续传拼出的完整文件 sha 才可能对上)
	if err := os.WriteFile(part, content[:50*1024], 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Download(&rel, dest, DownloadOptions{}); err != nil {
		t.Fatal(err)
	}
	if rangeHits.Load() == 0 {
		t.Fatal("未发出 Range 续传请求")
	}
	got, _ := os.ReadFile(dest)
	if len(got) != len(content) || sha256hex(got) != sha256hex(content) {
		t.Fatal("续传结果不完整")
	}
}

func TestDownloadCorruptResumeFallsBackToFull(t *testing.T) {
	content := fakeBin(3, 120*1024)
	srv := serveBinary(t, content, nil)
	rel := Release{Version: "1.0.0", Files: map[string]FileEntry{
		PlatformKey(): {URL: srv.URL, SHA256: sha256hex(content)},
	}}
	dir := t.TempDir()
	dest := filepath.Join(dir, "app.bin")

	// 预置一段"坏前缀" (内容对不上源): 续传拼出的文件 sha 必错,
	// 期望自动退回全量重下并成功
	os.WriteFile(dest+".part", fakeBin(9, 30*1024), 0o755)
	if err := Download(&rel, dest, DownloadOptions{}); err != nil {
		t.Fatalf("应自动全量重下成功: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if sha256hex(got) != sha256hex(content) {
		t.Fatal("全量重下后内容不符")
	}
}

func TestDownloadRejectsWrongPlatformAndBadHash(t *testing.T) {
	dir := t.TempDir()
	t.Run("平台缺失", func(t *testing.T) {
		rel := Release{Version: "1.0.0", Files: map[string]FileEntry{"beos/ppc": {URL: "http://x"}}}
		if err := Download(&rel, filepath.Join(dir, "a"), DownloadOptions{}); err == nil {
			t.Fatal("期望报平台缺失错误")
		}
	})
	t.Run("sha 不符", func(t *testing.T) {
		content := fakeBin(4, 8192)
		srv := serveBinary(t, content, nil)
		rel := Release{Version: "1.0.0", Files: map[string]FileEntry{
			PlatformKey(): {URL: srv.URL, SHA256: sha256hex(fakeBin(5, 8192))},
		}}
		t.Setenv("GOX_UPDATE_NO_RESUME", "1")
		if err := Download(&rel, filepath.Join(dir, "b"), DownloadOptions{}); err == nil {
			t.Fatal("期望 sha 校验失败")
		}
		if _, err := os.Stat(filepath.Join(dir, "b")); !os.IsNotExist(err) {
			t.Fatal("校验失败的最终文件不应存在")
		}
	})
}

// ===== Apply / CleanupBackup / Repair: 三段换名与中断自愈 =====

func writeBin(t *testing.T, p string, content []byte) {
	t.Helper()
	if err := os.WriteFile(p, content, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestApplyThreeStageRename(t *testing.T) {
	dir := t.TempDir()
	cur := filepath.Join(dir, "app")
	newBin := filepath.Join(dir, "app.new")
	writeBin(t, cur, fakeBin(1, 100))
	writeBin(t, newBin, fakeBin(2, 100))

	if err := Apply(cur, newBin); err != nil {
		t.Fatal(err)
	}
	gotCur, _ := os.ReadFile(cur)
	gotBak, _ := os.ReadFile(cur + ".bak")
	if string(gotCur) != string(fakeBin(2, 100)) {
		t.Fatal("当前版本未换新")
	}
	if string(gotBak) != string(fakeBin(1, 100)) {
		t.Fatal("备份内容不符")
	}
	if _, err := os.Stat(newBin); !os.IsNotExist(err) {
		t.Fatal(".new 应已被 rename 走")
	}

	// 下次启动清理 .bak
	if err := CleanupBackup(cur); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cur + ".bak"); !os.IsNotExist(err) {
		t.Fatal(".bak 未被清理")
	}
	// CleanupBackup 幂等: 再调一次不报错
	if err := CleanupBackup(cur); err != nil {
		t.Fatal(err)
	}
}

// TestApplyInterruptRollback 对应看板 rTkaiU (RELEASE_NOTES 已知问题 7 的
// 验证补课): 阶段 1 完成、阶段 2 失败 (对应"新文件丢失/磁盘异常/中途被杀")
// 时必须自愈 —— 当前版本原样可用, 不留半残状态。
func TestApplyInterruptRollback(t *testing.T) {
	dir := t.TempDir()
	cur := filepath.Join(dir, "app")
	missing := filepath.Join(dir, "app.missing") // 阶段 2 的源不存在
	writeBin(t, cur, fakeBin(1, 100))

	err := Apply(cur, missing)
	if err == nil {
		t.Fatal("阶段 2 失败应报错")
	}
	gotCur, _ := os.ReadFile(cur)
	if string(gotCur) != string(fakeBin(1, 100)) {
		t.Fatal("回滚后当前版本应是旧内容")
	}
	if _, err := os.Stat(cur + ".bak"); !os.IsNotExist(err) {
		t.Fatal("自愈成功后不应残留 .bak")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("源文件不应被 Apply 凭空产出")
	}
}

// TestRepairAfterPowerCut 模拟更极端的"阶段 1 之后断电": 磁盘上只剩
// .bak (当前版本已被改名走)。Repair 必须能把它恢复回当前版本。
func TestRepairAfterPowerCut(t *testing.T) {
	dir := t.TempDir()
	cur := filepath.Join(dir, "app")
	writeBin(t, cur+".bak", fakeBin(1, 100)) // 断电残局: 只有 .bak

	if err := Repair(cur); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(cur)
	if string(got) != string(fakeBin(1, 100)) {
		t.Fatal("Repair 后当前版本未恢复")
	}
	if _, err := os.Stat(cur + ".bak"); !os.IsNotExist(err) {
		t.Fatal("恢复后 .bak 应消失")
	}
	// 健康目录上 Repair 是 no-op
	if err := Repair(cur); err != nil {
		t.Fatal(err)
	}
}

// ===== SelfUpdate 端到端 =====

func TestSelfUpdateEndToEnd(t *testing.T) {
	oldBin, newBin := fakeBin(1, 512), fakeBin(2, 768)
	dir := t.TempDir()
	exe := filepath.Join(dir, "gox")
	writeBin(t, exe, oldBin)

	binSrv := serveBinary(t, newBin, nil)
	manifest := serveManifest(t, manifestJSON(t, Release{
		Version: "0.8.0",
		Channel: "stable",
		Files: map[string]FileEntry{
			PlatformKey(): {URL: binSrv.URL, SHA256: sha256hex(newBin), Size: int64(len(newBin))},
		},
	}))

	rel, applied, err := SelfUpdate(exe, manifest.URL, "0.7.0", CheckOptions{}, nil)
	if err != nil || !applied || rel == nil || rel.Version != "0.8.0" {
		t.Fatalf("SelfUpdate 异常: rel=%v applied=%v err=%v", rel, applied, err)
	}
	got, _ := os.ReadFile(exe)
	if string(got) != string(newBin) {
		t.Fatal("更新后二进制未就位")
	}
	if b, _ := os.ReadFile(exe + ".bak"); string(b) != string(oldBin) {
		t.Fatal("旧版本备份缺失")
	}
	// 模拟下一次启动: Repair + CleanupBackup 后目录干净可跑
	if err := Repair(exe); err != nil {
		t.Fatal(err)
	}
	if err := CleanupBackup(exe); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(exe + ".bak"); !os.IsNotExist(err) {
		t.Fatal("启动清理后 .bak 应消失")
	}

	// 已是最新: applied=false 且无动作
	_, applied2, err := SelfUpdate(exe, manifest.URL, "0.8.0", CheckOptions{}, nil)
	if err != nil || applied2 {
		t.Fatalf("已是最新时不应有动作: applied=%v err=%v", applied2, err)
	}
}

// TestManifestPayloadIsSmallGuard 清单体积护栏: LoadManifest 上限 8MB,
// 防呆"把二进制 url 误填成清单"时整包吃进内存 (对齐 rOxQ5f 的内存诉求)。
func TestManifestPayloadIsSmallGuard(t *testing.T) {
	big := make([]byte, 9<<20) // 9MB 超限
	rand.Read(big)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(big)
	}))
	defer srv.Close()
	if _, err := LoadManifest(srv.URL, CheckOptions{}); err == nil {
		t.Fatal("超限清单应被拒收")
	}
}
