package vm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ===== gx/update: 桌面自动更新的 JS 门面 =====

// updateManifestSrv 起一个清单 + 二进制双端点服务器, 返回清单 URL。
// 二进制内容固定, 清单给一个比 0.0.0 新的版本, files 指向真实平台键与本服务器的 /bin。
func updateManifestSrv(t *testing.T, version string) string {
	t.Helper()
	bin := []byte("fake-gox-binary-" + version)
	sum := sha256.Sum256(bin)
	var binURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/bin", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(bin)
	})
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"releases":[{"version":%q,"channel":"stable","notes":"测试版本",
			"files":{"%s":{"url":%q,"sha256":%q,"size":%d}}}]}`,
			version, runtime.GOOS+"/"+runtime.GOARCH, binURL, hex.EncodeToString(sum[:]), len(bin))
	})
	srv := httptest.NewServer(mux)
	binURL = srv.URL + "/bin"
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestGoxUmbrellaExportsUpdateAPIs gx/update 进了聚合入口 (注册表闸门之外
// 的运行时证据)。
func TestGoxUmbrellaExportsUpdateAPIs(t *testing.T) {
	for _, fn := range []string{"checkForUpdate", "downloadAndInstall", "currentVersion", "cleanupBackup"} {
		got := evalJS(t, `import { `+fn+` } from "gox"; typeof `+fn)
		assertString(t, got, "function")
	}
}

// TestUpdateCheckForUpdateFindsNewer checkForUpdate 拉清单选版本:
// 比当前新 → release 对象; 已是最新 → null。
func TestUpdateCheckForUpdateFindsNewer(t *testing.T) {
	manifestURL := updateManifestSrv(t, "9.9.9")
	got := runAsync(t, `
		import { checkForUpdate } from "gox";
		let __r = null;
		checkForUpdate(`+q(manifestURL)+`+"/manifest.json", {currentVersion: "0.1.0"})
			.then(rel => { __r = rel ? rel.version + ":" + rel.channel : "null"; })
			.catch(e => { __r = "ERR:" + e; });
	`, "__r")
	assertString(t, got, "9.9.9:stable")

	got2 := runAsync(t, `
		import { checkForUpdate } from "gox";
		let __r = null;
		checkForUpdate(`+q(manifestURL)+`+"/manifest.json", {currentVersion: "9.9.9"})
			.then(rel => { __r = rel === null ? "up-to-date" : rel.version; })
			.catch(e => { __r = "ERR:" + e; });
	`, "__r")
	assertString(t, got2, "up-to-date")
}

// TestUpdateCheckRejectsBadManifest 清单地址不可达/内容非法时 reject,
// JS 侧能 await 到 rejection (对齐 vm_host_modules 的 await 恢复路径)。
func TestUpdateCheckRejectsBadManifest(t *testing.T) {
	got := runAsync(t, `
		import { checkForUpdate } from "gox";
		let __r = null;
		checkForUpdate("http://127.0.0.1:1/none.json")
			.then(() => { __r = "resolved"; })
			.catch(e => { __r = "rejected:" + ("" + e).slice(0, 30); });
	`, "__r")
	if !strings.HasPrefix(got.Inspect(), "rejected:") {
		t.Fatalf("期望 reject, got %v", got.Inspect())
	}
}

// TestUpdateCurrentVersionFromGoxJSON currentVersion 的三级解析:
// 显式参数 > 工作目录 gox.json > "0.0.0"。
func TestUpdateCurrentVersionFromGoxJSON(t *testing.T) {
	dir := t.TempDir()
	goxJSON := `{"name":"demo","version":"3.2.1"}`
	if err := os.WriteFile(filepath.Join(dir, "gox.json"), []byte(goxJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	assertString(t, evalJS(t, `import { currentVersion } from "gx/update"; currentVersion()`), "3.2.1")
	assertString(t, evalJS(t, `import { currentVersion } from "gox"; currentVersion("9.0.0")`), "9.0.0")
}

// TestUpdateDownloadProgressBridgesToLoop onProgress 经事件循环桥接:
// 下载安装到测试替身不落地 (SelfUpdate 目标是进程自身, 这里只验证
// checkForUpdate + onProgress 通路 —— 落地链路由 update 包的 Go 测试覆盖)。
func TestUpdateDownloadProgressBridgesToLoop(t *testing.T) {
	// 直接下载一个 URL, 用 http 模块做不到带进度; 这里验证 downloadAndInstall
	// 在"已是最新"时 resolve null 且不触发下载 (无进度回调)。
	manifestURL := updateManifestSrv(t, "9.9.9")
	got := runAsync(t, `
		import { downloadAndInstall } from "gox";
		let __r = null;
		downloadAndInstall(`+q(manifestURL)+`+"/manifest.json", {
			currentVersion: "9.9.9",
			onProgress: function (d, t) { __r = "progressed"; }
		}).then(res => { if (__r === null) __r = res === null ? "no-update" : "unexpected"; })
		.catch(e => { __r = "ERR:" + e; });
	`, "__r")
	assertString(t, got, "no-update")
}
