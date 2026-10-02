package main

// gox-npm v0 —— 极简 npm 安装器。
//
// 定位（见 docs/gox-npm.md）: npm/ 子模块是**二进制分发器**（装 goxjs 命令），
// 本命令是**包安装器**（把纯 JS 依赖装进 node_modules 供 VM import）。
// 两者互不替代。
//
// 能力边界（v1，写在注释里避免后来者当 bug）:
//   - 只装直接依赖，不做传递依赖树 / 无 workspace / 无 devDependencies 区分；
//   - 不做 CommonJS：装下来的包若 require() 会在运行期报错（见 docs/npm-compat.md）；
//   - semver 只支持 ^ ~ 精确 与空格分隔的比较器，复杂 range 直接报错；
//   - 网络入口（registry / http.Client）都是字段，测试整体替换成 httptest.Server，
//     保证测试不联网。

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultNPMRegistry = "https://registry.npmjs.org"
	lockFileName       = "gox-lock.json"
	lockfileVersion    = 1
	// maxTarballBytes 限制单个 tarball 大小，防止恶意 packument 指向超大文件。
	maxTarballBytes = 256 << 20
	// maxPackumentBytes 限制 packument（含全部版本元信息）大小。
	maxPackumentBytes = 64 << 20
)

// npmClient 封装 registry 访问与本地 tarball 缓存。
type npmClient struct {
	registry string      // 无尾斜杠的 registry base URL
	http     *http.Client
	cacheDir string // ~/.gox/pkg-cache，按 <name>/<version>.tgz 分层
}

// newNPMClient 从环境构造客户端。
//
//	GOX_NPM_REGISTRY  覆盖 registry（内网镜像 / 测试）
//	GOX_PKG_CACHE     覆盖缓存目录（测试隔离用；正式使用无需设置）
func newNPMClient() *npmClient {
	reg := strings.TrimSpace(os.Getenv("GOX_NPM_REGISTRY"))
	if reg == "" {
		reg = defaultNPMRegistry
	}
	reg = strings.TrimRight(reg, "/")

	cache := strings.TrimSpace(os.Getenv("GOX_PKG_CACHE"))
	if cache == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			home = "."
		}
		cache = filepath.Join(home, ".gox", "pkg-cache")
	}
	return &npmClient{
		registry: reg,
		http:     &http.Client{Timeout: 60 * time.Second},
		cacheDir: cache,
	}
}

// ===== registry 元信息 =====

type pkgVersionMeta struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Dist    struct {
		Tarball   string `json:"tarball"`
		Integrity string `json:"integrity"`
	} `json:"dist"`
}

type packument struct {
	Name     string                    `json:"name"`
	DistTags map[string]string         `json:"dist-tags"`
	Versions map[string]pkgVersionMeta `json:"versions"`
}

// fetchPackument 拉取一个包的 packument（GET <registry>/<name>）。
// scoped 包名里的 "/" 转义为 %2F，与 npm 官方客户端一致。
func (c *npmClient) fetchPackument(name string) (*packument, error) {
	endpoint := c.registry + "/" + url.PathEscape(name)
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.npm.install-v1+json")
	req.Header.Set("User-Agent", "gox-npm/"+version)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("访问 registry 失败 (%s): %w\n  提示: 内网镜像可用 GOX_NPM_REGISTRY 覆盖", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("registry 上找不到包 %q (registry=%s)", name, c.registry)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry 返回 %d (%s)", resp.StatusCode, endpoint)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPackumentBytes))
	if err != nil {
		return nil, fmt.Errorf("读取 packument 失败: %w", err)
	}
	var p packument
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("解析 packument 失败 (%s): %w", name, err)
	}
	if len(p.Versions) == 0 {
		return nil, fmt.Errorf("packument %q 没有任何可用版本", name)
	}
	return &p, nil
}

// ===== 简化 semver =====

type semver struct {
	major, minor, patch int
	pre                 string // 预发布标识（不含 '-'），空串表示正式版
}

func (v semver) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
	if v.pre != "" {
		s += "-" + v.pre
	}
	return s
}

// parseSemver 解析 "1.2.3" / "v1.2.3" / "1.2.3-beta.1"。非三段数字返回 false。
func parseSemver(s string) (semver, bool) {
	s = strings.TrimSpace(strings.TrimPrefix(s, "v"))
	if i := strings.IndexByte(s, '+'); i >= 0 { // 去掉 build metadata
		s = s[:i]
	}
	pre := ""
	if i := strings.IndexByte(s, '-'); i >= 0 {
		pre = s[i+1:]
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	maj, e1 := strconv.Atoi(parts[0])
	min, e2 := strconv.Atoi(parts[1])
	pat, e3 := strconv.Atoi(parts[2])
	if e1 != nil || e2 != nil || e3 != nil {
		return semver{}, false
	}
	return semver{maj, min, pat, pre}, true
}

// compareSemver 返回 -1/0/1。预发布版 < 同号正式版（semver 规则）。
func compareSemver(a, b semver) int {
	if c := cmpInt(a.major, b.major); c != 0 {
		return c
	}
	if c := cmpInt(a.minor, b.minor); c != 0 {
		return c
	}
	if c := cmpInt(a.patch, b.patch); c != 0 {
		return c
	}
	switch {
	case a.pre == "" && b.pre == "":
		return 0
	case a.pre == "":
		return 1 // 正式版 > 预发布版
	case b.pre == "":
		return -1
	default:
		return strings.Compare(a.pre, b.pre)
	}
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// selectVersion 按简化 semver 口径从 packument 选出最高匹配版本。
//
// v1 支持的 range（够用即可）:
//
//	""/"*"/"latest"   → dist-tags.latest（缺失则取最高版本）
//	"1.2.3"           → 精确（含预发布后缀需完全一致）
//	"^1.2.3"          → >=1.2.3 <下一个 major；0.x 时按 npm 规则收紧
//	"~1.2.3"          → >=1.2.3 <1.(minor+1).0
//	">=1.0.0 <2.0.0"  → 空格分隔的比较器 AND（>= > <= < =）
//
// 不支持: ||、x 通配、hyphen range、预发布参与普通 range —— 这类 range
// 匹配结果可能为空并报错，不会静默装错版本。
func selectVersion(p *packument, rng string) (string, error) {
	rng = strings.TrimSpace(rng)
	if rng == "" || rng == "*" || rng == "latest" {
		if latest, ok := p.DistTags["latest"]; ok {
			if _, ok := p.Versions[latest]; ok {
				return latest, nil
			}
		}
		// 无 dist-tags 或指向不存在的版本 → 取最高版本
		best := ""
		var bestSV semver
		for vs := range p.Versions {
			sv, ok := parseSemver(vs)
			if !ok {
				continue
			}
			if best == "" || compareSemver(sv, bestSV) > 0 {
				best, bestSV = vs, sv
			}
		}
		if best == "" {
			return "", fmt.Errorf("无法确定最新版本")
		}
		return best, nil
	}

	best := ""
	var bestSV semver
	for vs := range p.Versions {
		sv, ok := parseSemver(vs)
		if !ok {
			continue
		}
		if !matchesRange(sv, rng) {
			continue
		}
		if best == "" || compareSemver(sv, bestSV) > 0 {
			best, bestSV = vs, sv
		}
	}
	if best == "" {
		return "", fmt.Errorf("registry 上没有任何版本满足 range %q", rng)
	}
	return best, nil
}

// matchesRange 判断版本是否满足空格分隔的多比较器 AND。
func matchesRange(v semver, rng string) bool {
	for _, part := range strings.Fields(rng) {
		if !matchComparator(v, part) {
			return false
		}
	}
	return true
}

func matchComparator(v semver, c string) bool {
	switch {
	case strings.HasPrefix(c, "^"):
		base, ok := parseSemver(c[1:])
		if !ok || !preAllowed(v, base) {
			return false
		}
		if compareSemver(v, base) < 0 {
			return false
		}
		var upper semver
		switch {
		case base.major > 0:
			upper = semver{major: base.major + 1}
		case base.minor > 0:
			upper = semver{minor: base.minor + 1}
		default:
			upper = semver{patch: base.patch + 1}
		}
		return compareSemver(v, upper) < 0
	case strings.HasPrefix(c, "~"):
		base, ok := parseSemver(c[1:])
		if !ok || !preAllowed(v, base) {
			return false
		}
		if compareSemver(v, base) < 0 {
			return false
		}
		return compareSemver(v, semver{major: base.major, minor: base.minor + 1}) < 0
	case strings.HasPrefix(c, ">="):
		base, ok := parseSemver(c[2:])
		return ok && preAllowed(v, base) && compareSemver(v, base) >= 0
	case strings.HasPrefix(c, "<="):
		base, ok := parseSemver(c[2:])
		return ok && preAllowed(v, base) && compareSemver(v, base) <= 0
	case strings.HasPrefix(c, ">"):
		base, ok := parseSemver(c[1:])
		return ok && preAllowed(v, base) && compareSemver(v, base) > 0
	case strings.HasPrefix(c, "<"):
		base, ok := parseSemver(c[1:])
		return ok && preAllowed(v, base) && compareSemver(v, base) < 0
	case strings.HasPrefix(c, "="):
		base, ok := parseSemver(c[1:])
		return ok && compareSemver(v, base) == 0
	default:
		base, ok := parseSemver(c)
		return ok && compareSemver(v, base) == 0
	}
}

// preAllowed 实现 semver 的预发布过滤: 普通 range 不匹配预发布版本，
// 除非该 range 的比较器本身带预发布（此时才允许同号预发布参与比较）。
func preAllowed(v, base semver) bool {
	if v.pre == "" {
		return true
	}
	return base.pre != ""
}

// ===== 缓存 + 下载 + 校验 =====

// lockEntry 是 gox-lock.json 里一条依赖记录。
type lockEntry struct {
	Version   string `json:"version"`
	Resolved  string `json:"resolved"`
	Integrity string `json:"integrity,omitempty"`
}

// ensureTarball 返回本地 tarball 路径，必要时下载。
// 缓存命中不重复下载；命中后仍校验 integrity（缓存可能损坏）。
func (c *npmClient) ensureTarball(meta pkgVersionMeta) (string, error) {
	if meta.Name == "" || meta.Version == "" {
		return "", fmt.Errorf("包元信息缺少 name/version")
	}
	cachePath := filepath.Join(c.cacheDir, filepath.FromSlash(meta.Name), meta.Version+".tgz")
	if fi, err := os.Stat(cachePath); err == nil && !fi.IsDir() {
		data, rerr := os.ReadFile(cachePath)
		if rerr != nil {
			return "", fmt.Errorf("读缓存失败 %s: %w", cachePath, rerr)
		}
		if err := verifyIntegrity(data, meta.Dist.Integrity, meta.Name, meta.Version); err != nil {
			return "", err
		}
		return cachePath, nil
	}

	if meta.Dist.Tarball == "" {
		return "", fmt.Errorf("包 %s@%s 的 dist.tarball 为空，无法下载", meta.Name, meta.Version)
	}
	data, err := c.get(meta.Dist.Tarball, maxTarballBytes)
	if err != nil {
		return "", fmt.Errorf("下载 %s@%s 失败: %w", meta.Name, meta.Version, err)
	}
	if err := verifyIntegrity(data, meta.Dist.Integrity, meta.Name, meta.Version); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return "", fmt.Errorf("创建缓存目录失败: %w", err)
	}
	if err := os.WriteFile(cachePath, data, 0o644); err != nil {
		return "", fmt.Errorf("写缓存失败: %w", err)
	}
	return cachePath, nil
}

// get 执行一次 GET 并读回全部字节（带上限）。
func (c *npmClient) get(rawURL string, limit int64) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "gox-npm/"+version)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// verifyIntegrity 校验 sha512 integrity。空或无 sha512 前缀时跳过（v1 只校验
// sha512 —— npm 现在默认就是 sha512）。校验失败必须报错，绝不静默放行。
func verifyIntegrity(data []byte, integrity, name, ver string) error {
	if integrity == "" {
		return nil
	}
	const prefix = "sha512-"
	if !strings.HasPrefix(integrity, prefix) {
		return nil // sha1/其它算法: v1 不校验
	}
	want := integrity[len(prefix):]
	sum := sha512.Sum512(data)
	got := base64.StdEncoding.EncodeToString(sum[:])
	if got != want {
		return fmt.Errorf("integrity 校验失败: %s@%s\n  期望 sha512-%s\n  实际 sha512-%s\n  (缓存可能损坏或 registry 内容被改动)", name, ver, want, got)
	}
	return nil
}

// ===== 解包 =====

// extractTarball 把 npm tarball 解包到 dest。
// npm tarball 顶层统一是 "package/" 前缀，这里剥掉。防目录穿越：任何解析后
// 逃出 dest 的条目直接报错（zip-slip）。符号链接等特殊条目 v1 跳过不落地。
func extractTarball(tgzPath, dest string) error {
	f, err := os.Open(tgzPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip 打不开: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	// 先清空目标目录，避免旧版本残留文件（如已删除的旧模块）。
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("读 tar 失败: %w", err)
		}
		name := strings.TrimPrefix(hdr.Name, "package/")
		if name == "" || name == "." || name == "/" {
			continue
		}
		target, err := safeExtractPath(dest, name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(hdr.Mode) & 0o777
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, io.LimitReader(tr, maxTarballBytes)); err != nil {
				out.Close()
				return err
			}
			out.Close()
		default:
			// 符号链接 / 设备文件等: v1 跳过（npm 包里罕见，且落地有安全风险）
		}
	}
	return nil
}

// safeExtractPath 把 tarball 内的相对名解析到 dest 之下，越界即报错。
func safeExtractPath(dest, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("tarball 含绝对路径条目: %s", name)
	}
	target := filepath.Join(dest, clean)
	rel, err := filepath.Rel(dest, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("tarball 含越界路径条目: %s", name)
	}
	return target, nil
}

// ===== 安装流程 =====

type installSpec struct {
	name string
	rng  string
}

// installOne 解析 → 下载（或命中缓存）→ 解包到 node_modules/<name>。
func (c *npmClient) installOne(dir, name, rng string) (lockEntry, error) {
	p, err := c.fetchPackument(name)
	if err != nil {
		return lockEntry{}, err
	}
	ver, err := selectVersion(p, rng)
	if err != nil {
		return lockEntry{}, fmt.Errorf("%s@%s: %w", name, rng, err)
	}
	meta := p.Versions[ver]
	tgz, err := c.ensureTarball(meta)
	if err != nil {
		return lockEntry{}, err
	}
	dest := filepath.Join(dir, "node_modules", filepath.FromSlash(name))
	if err := extractTarball(tgz, dest); err != nil {
		return lockEntry{}, fmt.Errorf("解包 %s@%s: %w", name, ver, err)
	}
	return lockEntry{Version: ver, Resolved: meta.Dist.Tarball, Integrity: meta.Dist.Integrity}, nil
}

// installAll 依次安装 specs，返回本次 lock 记录。
// 按包名排序，保证 lock / 输出顺序稳定（map 迭代随机）。
func (c *npmClient) installAll(dir string, specs []installSpec) (map[string]lockEntry, error) {
	sort.Slice(specs, func(i, j int) bool { return specs[i].name < specs[j].name })
	lock := map[string]lockEntry{}
	for _, s := range specs {
		entry, err := c.installOne(dir, s.name, s.rng)
		if err != nil {
			return nil, err
		}
		lock[s.name] = entry
		fmt.Printf("  + %s@%s\n", s.name, entry.Version)
	}
	return lock, nil
}

// ===== package.json / lock 读写 =====

// readPackageJSONFile 读取目录下的 package.json；不存在返回空对象。
func readPackageJSONFile(dir string) (map[string]any, error) {
	path := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("package.json 解析失败: %w", err)
	}
	return m, nil
}

// writeJSONFile 以 2 空格缩进 + 末尾换行写 JSON（与 npm 一致）。
func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// readLock 读取 gox-lock.json；不存在返回空 lock。
func readLock(dir string) (map[string]lockEntry, error) {
	path := filepath.Join(dir, lockFileName)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]lockEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	var lf struct {
		Packages map[string]lockEntry `json:"packages"`
	}
	if err := json.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("%s 解析失败: %w", lockFileName, err)
	}
	if lf.Packages == nil {
		lf.Packages = map[string]lockEntry{}
	}
	return lf.Packages, nil
}

// writeLock 写出 gox-lock.json。
func writeLock(dir string, lock map[string]lockEntry) error {
	lf := struct {
		LockfileVersion int                  `json:"lockfileVersion"`
		Packages        map[string]lockEntry `json:"packages"`
	}{LockfileVersion: lockfileVersion, Packages: lock}
	return writeJSONFile(filepath.Join(dir, lockFileName), lf)
}

// depsFromPackageJSON 取出 dependencies（只认 dependencies，v1 不看 devDependencies）。
func depsFromPackageJSON(pj map[string]any) []installSpec {
	raw, ok := pj["dependencies"].(map[string]any)
	if !ok {
		return nil
	}
	specs := make([]installSpec, 0, len(raw))
	for name, v := range raw {
		specs = append(specs, installSpec{name: name, rng: fmt.Sprint(v)})
	}
	return specs
}

// parseAddSpec 拆分 `pkg[@range]`。作用域包 `@scope/pkg@range` 取最后一个 '@'。
func parseAddSpec(s string) (name, rng string) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "@") {
		slash := strings.IndexByte(s, '/')
		if i := strings.LastIndexByte(s, '@'); i > 0 && i > slash {
			return s[:i], s[i+1:]
		}
		return s, ""
	}
	if i := strings.LastIndexByte(s, '@'); i > 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// ===== 命令入口 =====

// runNpmInstall 实现 `gox install [pkg[@range]...]`。
// 无参数时按 package.json 的 dependencies 安装；带参数时等价于 `gox add`。
func runNpmInstall(args []string) {
	dir, err := os.Getwd()
	if err != nil {
		npmFatal("install", err.Error())
	}
	if len(args) > 0 {
		runNpmAdd(args)
		return
	}
	if err := npmInstallCore(dir, newNPMClient()); err != nil {
		npmFatal("install", err.Error())
	}
}

// npmInstallCore 是 `gox install` 的可测核心（不碰 os.Exit / os.Getwd）。
func npmInstallCore(dir string, c *npmClient) error {
	pj, err := readPackageJSONFile(dir)
	if err != nil {
		return err
	}
	specs := depsFromPackageJSON(pj)
	if len(specs) == 0 {
		fmt.Println("package.json 里没有 dependencies，无需安装。")
		fmt.Println("加依赖: gox add <pkg>[@<range>]")
		return nil
	}

	fmt.Printf("从 %s 安装 %d 个依赖...\n", c.registry, len(specs))
	lock, err := c.installAll(dir, specs)
	if err != nil {
		return err
	}
	if err := writeLock(dir, lock); err != nil {
		return err
	}
	fmt.Printf("完成。已写入 %s（缓存: %s）\n", lockFileName, c.cacheDir)
	return nil
}

// runNpmAdd 实现 `gox add <pkg>[@<range>] ...`。
func runNpmAdd(args []string) {
	dir, err := os.Getwd()
	if err != nil {
		npmFatal("add", err.Error())
	}
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, "用法: gox add <pkg>[@<range>] ...\n\n示例: gox add lodash@^4\n     gox add @scope/pkg\n")
		os.Exit(2)
	}
	if err := npmAddCore(dir, newNPMClient(), args); err != nil {
		npmFatal("add", err.Error())
	}
}

// npmAddCore 是 `gox add` 的可测核心。
func npmAddCore(dir string, c *npmClient, args []string) error {
	pj, err := readPackageJSONFile(dir)
	if err != nil {
		return err
	}
	deps, _ := pj["dependencies"].(map[string]any)
	if deps == nil {
		deps = map[string]any{}
	}

	// 先解析全部 spec（名称 + range），再逐个安装并回填 dependencies / lock。
	specs := make([]installSpec, 0, len(args))
	for _, a := range args {
		name, rng := parseAddSpec(a)
		if name == "" {
			return fmt.Errorf("非法包名: %s", a)
		}
		specs = append(specs, installSpec{name: name, rng: rng})
	}

	fmt.Printf("从 %s 安装...\n", c.registry)
	newLock, err := c.installAll(dir, specs)
	if err != nil {
		return err
	}

	// 回填 dependencies: 用户给了 range 就用它，否则写 "^<选中版本>"（npm 默认口径）。
	for _, s := range specs {
		if s.rng != "" {
			deps[s.name] = s.rng
		} else {
			deps[s.name] = "^" + newLock[s.name].Version
		}
	}
	pj["dependencies"] = deps
	if err := writeJSONFile(filepath.Join(dir, "package.json"), pj); err != nil {
		return err
	}

	// lock 与已存在的合并（不丢之前装的依赖）。
	lock, err := readLock(dir)
	if err != nil {
		return err
	}
	for name, entry := range newLock {
		lock[name] = entry
	}
	if err := writeLock(dir, lock); err != nil {
		return err
	}
	fmt.Printf("完成。已更新 package.json 与 %s（缓存: %s）\n", lockFileName, c.cacheDir)
	return nil
}

// npmFatal 打印 gox-npm 错误并退出。
func npmFatal(cmd, msg string) {
	fmt.Fprintf(os.Stderr, "gox %s: %s\n", cmd, msg)
	os.Exit(1)
}
