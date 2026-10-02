package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// ===== gox-npm 测试：全部用 httptest.Server 造假 registry，**不联网** =====

// makeTarball 造一个 npm 形态的 tgz（顶层 "package/" 前缀）。
func makeTarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		hdr := &tar.Header{
			Name:     "package/" + name,
			Mode:     0o644,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}
	return buf.Bytes()
}

// sha512Integrity 返回 npm 风格的 sha512-<base64>。
func sha512Integrity(data []byte) string {
	sum := sha512.Sum512(data)
	return "sha512-" + base64.StdEncoding.EncodeToString(sum[:])
}

// fakeRegistry 起一个只服务单个包 foo 的假 registry。
// foo 有 1.0.0 与 1.1.0 两个版本，dist-tags.latest = 1.1.0。
// 返回 server 与"tarball 被请求次数"的读数函数。
func fakeRegistry(t *testing.T, integrityOverride string) (*httptest.Server, func() int32) {
	t.Helper()
	tgz := makeTarball(t, map[string]string{
		"package.json": `{"name":"foo","version":"1.1.0","main":"index.js"}`,
		"index.js":     `export const hi = "foo-1.1.0";`,
	})
	integrity := sha512Integrity(tgz)
	if integrityOverride != "" {
		integrity = integrityOverride
	}

	var tgzReqs int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/foo":
			tarball := srv.URL + "/foo/-/foo-1.1.0.tgz"
			packument := map[string]any{
				"name":      "foo",
				"dist-tags": map[string]string{"latest": "1.1.0"},
				"versions": map[string]any{
					"1.0.0": map[string]any{
						"name": "foo", "version": "1.0.0",
						"dist": map[string]string{"tarball": srv.URL + "/foo/-/foo-1.0.0.tgz", "integrity": integrity},
					},
					"1.1.0": map[string]any{
						"name": "foo", "version": "1.1.0",
						"dist": map[string]string{"tarball": tarball, "integrity": integrity},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(packument)
		case "/foo/-/foo-1.1.0.tgz", "/foo/-/foo-1.0.0.tgz":
			atomic.AddInt32(&tgzReqs, 1)
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(tgz)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() int32 { return atomic.LoadInt32(&tgzReqs) }
}

// testClient 用假 registry 构造客户端，缓存目录指向临时目录。
func testClient(t *testing.T, srv *httptest.Server) *npmClient {
	t.Helper()
	return &npmClient{registry: srv.URL, http: srv.Client(), cacheDir: t.TempDir()}
}

func TestNpmAddDownloadsExtractsAndLocks(t *testing.T) {
	srv, _ := fakeRegistry(t, "")
	dir := t.TempDir()
	c := testClient(t, srv)

	if err := npmAddCore(dir, c, []string{"foo"}); err != nil {
		t.Fatalf("npmAddCore: %v", err)
	}

	// node_modules 落地
	idx := filepath.Join(dir, "node_modules", "foo", "index.js")
	data, err := os.ReadFile(idx)
	if err != nil {
		t.Fatalf("node_modules/foo/index.js 未落地: %v", err)
	}
	if !bytes.Contains(data, []byte("foo-1.1.0")) {
		t.Fatalf("解包内容不对: %s", data)
	}
	// 顶层 package/ 前缀被剥掉（不应出现 node_modules/foo/package/index.js）
	if _, err := os.Stat(filepath.Join(dir, "node_modules", "foo", "package")); err == nil {
		t.Fatal("tarball 的 package/ 前缀未被剥掉")
	}

	// package.json 的 dependencies
	pj, err := readPackageJSONFile(dir)
	if err != nil {
		t.Fatalf("读 package.json: %v", err)
	}
	deps, _ := pj["dependencies"].(map[string]any)
	if got := fmt.Sprint(deps["foo"]); got != "^1.1.0" {
		t.Fatalf("dependencies[foo] = %q, want ^1.1.0", got)
	}

	// gox-lock.json
	lock, err := readLock(dir)
	if err != nil {
		t.Fatalf("读 lock: %v", err)
	}
	entry, ok := lock["foo"]
	if !ok {
		t.Fatal("gox-lock.json 缺少 foo")
	}
	if entry.Version != "1.1.0" {
		t.Fatalf("lock version = %q, want 1.1.0", entry.Version)
	}
	if entry.Resolved == "" || entry.Integrity == "" {
		t.Fatalf("lock 缺 resolved/integrity: %+v", entry)
	}
}

func TestNpmAddRangeSelectsVersion(t *testing.T) {
	srv, _ := fakeRegistry(t, "")
	dir := t.TempDir()
	c := testClient(t, srv)

	// ~1.0.0 只匹配 1.0.0（1.1.0 越界）
	if err := npmAddCore(dir, c, []string{"foo@~1.0.0"}); err != nil {
		t.Fatalf("npmAddCore: %v", err)
	}
	lock, _ := readLock(dir)
	if lock["foo"].Version != "1.0.0" {
		t.Fatalf("~1.0.0 应选 1.0.0, got %q", lock["foo"].Version)
	}
}

// TestNpmInstallCacheHitSecondRun 是缓存行为的核心断言:
// 第一次下载，第二次（新的 node_modules 目标，同一缓存目录）不再请求 tarball。
func TestNpmInstallCacheHitSecondRun(t *testing.T) {
	srv, reqs := fakeRegistry(t, "")
	c := testClient(t, srv)

	dir1 := t.TempDir()
	if err := npmAddCore(dir1, c, []string{"foo"}); err != nil {
		t.Fatalf("第一次安装: %v", err)
	}
	if got := reqs(); got != 1 {
		t.Fatalf("第一次应下载 1 次, got %d", got)
	}

	// 换一个工程目录但共享同一 cacheDir —— 第二次必须命中缓存，不再下载。
	dir2 := t.TempDir()
	if err := npmAddCore(dir2, c, []string{"foo"}); err != nil {
		t.Fatalf("第二次安装: %v", err)
	}
	if got := reqs(); got != 1 {
		t.Fatalf("第二次应命中缓存(仍为 1 次下载), got %d", got)
	}
	// 但 node_modules 仍要落到新工程目录（缓存是 tarball 级，与解包分离）。
	if _, err := os.Stat(filepath.Join(dir2, "node_modules", "foo", "index.js")); err != nil {
		t.Fatalf("第二次 node_modules 未落地: %v", err)
	}
}

func TestNpmIntegrityMismatchFails(t *testing.T) {
	// 故意给错的 integrity（合法 base64 sha512 但内容不符）
	bad := "sha512-" + base64.StdEncoding.EncodeToString(make([]byte, 64))
	srv, _ := fakeRegistry(t, bad)
	dir := t.TempDir()
	c := testClient(t, srv)

	err := npmAddCore(dir, c, []string{"foo"})
	if err == nil {
		t.Fatal("integrity 不符必须报错")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("integrity 校验失败")) {
		t.Fatalf("报错应提到 integrity 校验失败, got: %v", err)
	}
	// 校验失败不应在 node_modules 留下解包结果
	if _, statErr := os.Stat(filepath.Join(dir, "node_modules", "foo")); statErr == nil {
		t.Fatal("校验失败后不应解包到 node_modules")
	}
}

func TestNpmInstallFromPackageJSON(t *testing.T) {
	srv, _ := fakeRegistry(t, "")
	dir := t.TempDir()
	c := testClient(t, srv)

	pj := `{"name":"app","version":"0.0.1","dependencies":{"foo":"^1.0.0"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pj), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := npmInstallCore(dir, c); err != nil {
		t.Fatalf("npmInstallCore: %v", err)
	}
	lock, _ := readLock(dir)
	if lock["foo"].Version != "1.1.0" {
		t.Fatalf("^1.0.0 应选 1.1.0, got %q", lock["foo"].Version)
	}
	// install 不应改写 dependencies 的 range
	pj2, _ := readPackageJSONFile(dir)
	deps, _ := pj2["dependencies"].(map[string]any)
	if fmt.Sprint(deps["foo"]) != "^1.0.0" {
		t.Fatalf("install 不应改写 range, got %q", deps["foo"])
	}
}

// ===== 无网络的纯单元测试 =====

func TestSelectVersionRanges(t *testing.T) {
	versions := []string{"0.9.0", "1.0.0", "1.0.5", "1.1.0", "1.1.2", "2.0.0", "2.0.1-beta.1"}
	p := &packument{DistTags: map[string]string{"latest": "2.0.0"}, Versions: map[string]pkgVersionMeta{}}
	for _, v := range versions {
		p.Versions[v] = pkgVersionMeta{Name: "x", Version: v}
	}
	cases := []struct {
		rng  string
		want string
	}{
		{"", "2.0.0"},
		{"*", "2.0.0"},
		{"latest", "2.0.0"},
		{"1.0.5", "1.0.5"},
		{"^1.0.0", "1.1.2"}, // <2.0.0
		{"~1.0.0", "1.0.5"}, // <1.1.0
		{"~1.1.0", "1.1.2"},
		{">=1.0.0 <2.0.0", "1.1.2"},
		{">1.1.2", "2.0.0"},
	}
	for _, tc := range cases {
		got, err := selectVersion(p, tc.rng)
		if err != nil {
			t.Fatalf("selectVersion(%q) error: %v", tc.rng, err)
		}
		if got != tc.want {
			t.Errorf("selectVersion(%q) = %q, want %q", tc.rng, got, tc.want)
		}
	}
	// 无匹配必须报错，不能静默装错
	if _, err := selectVersion(p, "^3.0.0"); err == nil {
		t.Error("无匹配 range 应报错")
	}
}

func TestSelectVersionCaretZero(t *testing.T) {
	// ^0.2.3 按 npm 规则 → >=0.2.3 <0.3.0
	p := &packument{Versions: map[string]pkgVersionMeta{}}
	for _, v := range []string{"0.2.3", "0.2.9", "0.3.0"} {
		p.Versions[v] = pkgVersionMeta{Version: v}
	}
	got, err := selectVersion(p, "^0.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if got != "0.2.9" {
		t.Fatalf("^0.2.3 = %q, want 0.2.9", got)
	}
}

func TestParseAddSpec(t *testing.T) {
	cases := []struct{ in, name, rng string }{
		{"lodash", "lodash", ""},
		{"lodash@^4", "lodash", "^4"},
		{"lodash@4.17.21", "lodash", "4.17.21"},
		{"@scope/pkg", "@scope/pkg", ""},
		{"@scope/pkg@^1.2.0", "@scope/pkg", "^1.2.0"},
	}
	for _, tc := range cases {
		n, r := parseAddSpec(tc.in)
		if n != tc.name || r != tc.rng {
			t.Errorf("parseAddSpec(%q) = (%q,%q), want (%q,%q)", tc.in, n, r, tc.name, tc.rng)
		}
	}
}

func TestSafeExtractPathRejectsTraversal(t *testing.T) {
	dest := t.TempDir()
	if _, err := safeExtractPath(dest, "../evil.js"); err == nil {
		t.Error("应拒绝 ../ 越界路径")
	}
	if _, err := safeExtractPath(dest, "../../etc/passwd"); err == nil {
		t.Error("应拒绝多级越界路径")
	}
	got, err := safeExtractPath(dest, "lib/index.js")
	if err != nil {
		t.Fatalf("正常路径不应报错: %v", err)
	}
	want := filepath.Join(dest, "lib", "index.js")
	if got != want {
		t.Fatalf("safeExtractPath = %q, want %q", got, want)
	}
}

func TestNewNPMClientEnvOverride(t *testing.T) {
	t.Setenv("GOX_NPM_REGISTRY", "https://mirror.example.com/")
	t.Setenv("GOX_PKG_CACHE", t.TempDir())
	c := newNPMClient()
	if c.registry != "https://mirror.example.com" {
		t.Fatalf("registry = %q, 应去掉尾斜杠", c.registry)
	}
}
