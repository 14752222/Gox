// Package update 实现 Gox 桌面应用的自动更新 (gx/update v1.1)。
//
// 设计文档: docs/auto-update.md。一句话概括:
//
//	清单(manifest) → 选版本(Check) → 流式下载(Download) → 三段换名(Apply) → 启动清理(CleanupBackup/Repair)
//
// v1.1 相比"整包进内存"的 v1 草案的三点增强 (对应看板 rOxQ5f):
//   - 流式下载落盘: 下载直接写 <dest>.part 文件, 内存占用 O(缓冲区) 不随包体增长;
//   - 断点续传: .part 已存在时带 Range 续传 (服务端不支持/校验失败则自动退回全量);
//   - 进度回调: ProgressFunc 周期回调 (done, total), 由上层桥接到 JS 事件循环;
//   - pre-release 通道: 清单带 channel 字段 ("stable"/"pre"), Check 按 options 放行。
//
// 换名策略 (Windows 友好): 正在运行的 exe 不能被覆盖写入, 但可以改名。
// 因此"当前版本 → .bak 备份、新版本 → 就位"全部用 rename, 原子性由文件系统保证。
// 中断自愈边界 (诚实声明, 对应看板 rTkaiU):
//   - 阶段 2 失败 (新文件缺失/磁盘问题): 自动把 .bak 改回当前版本, 应用照常可跑;
//   - 阶段 1 之后、阶段 2 之前断电: 留下 "只有 .bak" 的状态 —— 启动时调
//     Repair() 自动恢复; 应用自身第一次启动就调 Repair + CleanupBackup 即可闭环。
package update

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ChannelStable / ChannelPre 是清单里 release 的通道标记。
// stable 面向所有人; pre 只在 options.PreRelease=true 时参与挑选。
const (
	ChannelStable = "stable"
	ChannelPre    = "pre"
)

// FileEntry 描述单个平台产物: 下载地址 + 完整性校验 (二选一, sha256 优先)。
type FileEntry struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256,omitempty"`
	Size   int64  `json:"size,omitempty"`
}

// Release 是一次发布: 版本号 + 通道 + 各平台产物。
// Files 的键是 "goos/goarch" (runtime.GOOS + "/" + runtime.GOARCH), 例如
// "windows/amd64"、"darwin/arm64"、"linux/amd64"。
type Release struct {
	Version string               `json:"version"`
	Channel string               `json:"channel,omitempty"`
	Notes   string               `json:"notes,omitempty"`
	Files   map[string]FileEntry `json:"files"`
}

// Manifest 是托管在静态服务器上的清单文件。Releases 顺序无关 —— PickRelease
// 按 semver 挑最大的合规项。只保留最近几个版本即可, 历史清掉不影响升级。
type Manifest struct {
	Releases []Release `json:"releases"`
}

// CheckOptions 控制 Check 的通道与网络行为。
type CheckOptions struct {
	// PreRelease 为 true 时 pre 通道也参与选版本 (与 stable 一起比, 取最大)。
	PreRelease bool
	// HTTPClient 为 nil 时用包内默认客户端 (15s 超时)。
	HTTPClient *http.Client
}

// DownloadOptions 控制 Download 的进度回调与续传。
type DownloadOptions struct {
	// Progress 在主下载循环里周期回调 (done, total 字节数)。total 为 -1 表示
	// 服务端没给长度。进度节流由调用方决定; 本包每收到一块数据都会回调,
	// 频率上限是网络速度 —— 桥接 JS 的层应自行节流。
	Progress ProgressFunc
	// HTTPClient 为 nil 时用包内默认客户端。
	HTTPClient *http.Client
}

// ProgressFunc 是下载进度回调: done 已下载字节数, total 总字节数 (-1 未知)。
type ProgressFunc func(done, total int64)

// 默认 HTTP 客户端: 更新检查/下载都不该挂死应用, 统一给 15 分钟上限
// (大包 + 慢网也要能走完), 连接/响应头阶段由 http.Client 整体超时兜底。
var defaultClient = &http.Client{Timeout: 15 * time.Minute}

// PlatformKey 返回当前平台在 Release.Files 里的键。
func PlatformKey() string { return runtime.GOOS + "/" + runtime.GOARCH }

// LoadManifest 拉取并解析清单。
func LoadManifest(manifestURL string, o CheckOptions) (*Manifest, error) {
	if strings.TrimSpace(manifestURL) == "" {
		return nil, errors.New("update: 清单地址为空")
	}
	client := o.HTTPClient
	if client == nil {
		client = defaultClient
	}
	resp, err := client.Get(manifestURL)
	if err != nil {
		return nil, fmt.Errorf("update: 拉取清单失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: 清单返回 %s (%s)", resp.Status, manifestURL)
	}
	var m Manifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&m); err != nil {
		return nil, fmt.Errorf("update: 清单不是合法 JSON: %w", err)
	}
	return &m, nil
}

// Check 拉清单并选出比 current 更新的合规版本; 已是最新时返回 (nil, nil)。
// current 传 "0.0.0" (或解析不了的串) 视为"永远要更新", 这是全新安装自更新
// 的常见形态。
func Check(manifestURL, current string, o CheckOptions) (*Release, error) {
	m, err := LoadManifest(manifestURL, o)
	if err != nil {
		return nil, err
	}
	return PickRelease(m, current, o)
}

// PickRelease 在清单里挑"合规通道里 semver 最大的且 > current"的 release。
// 通道规则: stable 恒参与; pre 仅在 o.PreRelease 时参与。同版本号时 stable 优先
// (pre 通道重新发布同号 stable 视为转正)。
func PickRelease(m *Manifest, current string, o CheckOptions) (*Release, error) {
	if m == nil {
		return nil, errors.New("update: 清单为空")
	}
	cur, curOK := parseSemver(current)
	var best *Release
	var bestVer semver
	for i := range m.Releases {
		r := &m.Releases[i]
		ch := strings.ToLower(strings.TrimSpace(r.Channel))
		if ch == "" {
			ch = ChannelStable // 清单省略 channel 视为 stable (向后兼容 v1 清单)
		}
		if ch != ChannelStable && ch != ChannelPre {
			continue // 未知通道不参与, 避免上游笔误把所有人拖进灰度
		}
		if ch == ChannelPre && !o.PreRelease {
			continue
		}
		v, ok := parseSemver(r.Version)
		if !ok {
			continue // 版本号不合法的条目跳过, 不让一条脏数据废掉整个清单
		}
		// 越大越好; 同号 stable 压过 pre (semver 相等时用通道决胜)
		if best == nil || v.gt(bestVer) || (v.eq(bestVer) && ch == ChannelStable && best.Channel == ChannelPre) {
			best, bestVer = r, v
		}
	}
	if best == nil {
		return nil, fmt.Errorf("update: 清单里没有可用版本 (preRelease=%v)", o.PreRelease)
	}
	if bv, ok := parseSemver(best.Version); ok && curOK && !bv.gt(cur) {
		return nil, nil // 已是最新
	}
	return best, nil
}

// Download 把 release 里当前平台的产物流式下载到 destPath。
//
// 落盘过程: 先写 destPath.part (已存在则尝试 Range 续传), 下载完成后校验
// sha256 (清单给了才校验) 与 size, 全部通过才把 .part 原子改名为 destPath。
// 校验失败自动全量重下一次 (续传的文件可能来自旧版本), 再失败才报错 ——
// 保证调用方拿到的 destPath 要么是校验过的完整文件, 要么不存在。
func Download(rel *Release, destPath string, o DownloadOptions) error {
	if rel == nil {
		return errors.New("update: release 为空")
	}
	entry, ok := rel.Files[PlatformKey()]
	if !ok {
		return fmt.Errorf("update: 清单没有 %s 平台的产物 (有: %s)",
			PlatformKey(), strings.Join(platformKeys(rel.Files), ", "))
	}
	if strings.TrimSpace(entry.URL) == "" {
		return fmt.Errorf("update: %s 的产物 url 为空", PlatformKey())
	}
	client := o.HTTPClient
	if client == nil {
		client = defaultClient
	}

	partPath := destPath + ".part"
	expect, _ := hex.DecodeString(strings.ToLower(entry.SHA256))
	if len(expect) > 0 && len(expect) != sha256.Size {
		return fmt.Errorf("update: 清单 sha256 长度不是 %d", sha256.Size)
	}

	for attempt := 0; attempt < 2; attempt++ {
		resumed := attempt == 0 && o.resumeEnabled() && fileExists(partPath)
		err := downloadOnce(client, entry, partPath, resumed, o.Progress)
		if err != nil {
			if resumed {
				// 续传路径出错 (服务器不支持 Range/半截文件损坏): 清掉重来
				os.Remove(partPath)
				continue
			}
			return err
		}
		if len(expect) > 0 {
			sum, err := fileSHA256(partPath)
			if err != nil {
				return fmt.Errorf("update: 校验失败: %w", err)
			}
			if !bytes.Equal(sum, expect) {
				if attempt == 0 {
					os.Remove(partPath) // 续传拼出来的文件不对, 全量重下一次
					continue
				}
				return fmt.Errorf("update: sha256 校验失败 (期望 %s, 实际 %s)",
					entry.SHA256, hex.EncodeToString(sum))
			}
		}
		if entry.Size > 0 {
			if st, err := os.Stat(partPath); err == nil && st.Size() != entry.Size {
				return fmt.Errorf("update: 文件大小不符 (期望 %d, 实际 %d)", entry.Size, st.Size())
			}
		}
		return os.Rename(partPath, destPath)
	}
	return fmt.Errorf("update: 下载未能完成 (%s)", entry.URL)
}

// resumeEnabled 留一个开关位: 目前默认允许续传; 测试里用环境变量关掉,
// 模拟"服务器不支持 Range"的退化路径。
func (o DownloadOptions) resumeEnabled() bool { return os.Getenv("GOX_UPDATE_NO_RESUME") == "" }

func downloadOnce(client *http.Client, entry FileEntry, partPath string, resume bool, progress ProgressFunc) error {
	var start int64 = 0
	if resume {
		if st, err := os.Stat(partPath); err == nil {
			start = st.Size()
		}
	}
	req, err := http.NewRequest(http.MethodGet, entry.URL, nil)
	if err != nil {
		return fmt.Errorf("update: 构造请求失败: %w", err)
	}
	if start > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(start, 10)+"-")
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("update: 下载失败: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case start > 0 && resp.StatusCode == http.StatusPartialContent:
		// 续传成功, 追加写
	case resp.StatusCode == http.StatusOK:
		// 全量 (服务器不支持 Range 或本来就没有半截文件), 覆盖写
		start = 0
	default:
		return fmt.Errorf("update: 下载返回 %s (%s)", resp.Status, entry.URL)
	}

	flag := os.O_CREATE | os.O_WRONLY
	if start > 0 {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(partPath, flag, 0o755)
	if err != nil {
		return fmt.Errorf("update: 打开临时文件失败: %w", err)
	}
	defer f.Close()

	total := int64(-1)
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		// "bytes 1024-4095/4096" → 4096
		if i := strings.LastIndexByte(cr, '/'); i >= 0 {
			if v, err := strconv.ParseInt(cr[i+1:], 10, 64); err == nil {
				total = v
			}
		}
	} else if cl := resp.Header.Get("Content-Length"); cl != "" {
		if v, err := strconv.ParseInt(cl, 10, 64); err == nil {
			total = start + v
		}
	}
	if entry.Size > 0 {
		total = entry.Size // 清单给的 size 最可信
	}

	done := start
	buf := make([]byte, 64<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return fmt.Errorf("update: 写盘失败: %w", werr)
			}
			done += int64(n)
			if progress != nil {
				progress(done, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("update: 下载中断: %w", rerr)
		}
	}
	return f.Sync()
}

// Apply 用三段换名把 newBinaryPath 就位到 binaryPath。
//
//  1. binaryPath → binaryPath.bak   (备份当前版本)
//  2. newBinaryPath → binaryPath    (新版本就位)
//  3. 任一步失败: .bak 改回 binaryPath (自愈), 并返回带上下文的错误
//
// 成功后 .bak 刻意保留 (回滚窗口), 由下一次启动的 CleanupBackup 清掉。
func Apply(binaryPath, newBinaryPath string) error {
	bak := binaryPath + ".bak"
	if err := os.Rename(binaryPath, bak); err != nil {
		return fmt.Errorf("update: 备份当前版本失败: %w", err)
	}
	if err := os.Rename(newBinaryPath, binaryPath); err != nil {
		if rbErr := os.Rename(bak, binaryPath); rbErr != nil {
			return fmt.Errorf("update: 就位失败 (%v) 且自愈失败 (%v); 当前版本备份还在 %s", err, rbErr, bak)
		}
		return fmt.Errorf("update: 就位失败, 已回滚到当前版本: %w", err)
	}
	return nil
}

// CleanupBackup 清掉上次更新留下的 .bak 垃圾。启动时调用一次即可;
// 没有 .bak 时静默返回 (幂等)。
func CleanupBackup(binaryPath string) error {
	err := os.Remove(binaryPath + ".bak")
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("update: 清理备份失败: %w", err)
	}
	return nil
}

// Repair 处理"阶段 1 之后断电"的残局: 当前版本不在、.bak 还在 → 把 .bak 改回。
// 应用启动时可与 CleanupBackup 一起调用; 目录健康时是 no-op。
func Repair(binaryPath string) error {
	if fileExists(binaryPath) {
		return nil
	}
	bak := binaryPath + ".bak"
	if !fileExists(bak) {
		return nil // 没有残局, no-op
	}
	if err := os.Rename(bak, binaryPath); err != nil {
		return fmt.Errorf("update: 自愈恢复失败 (%s → %s): %w", bak, binaryPath, err)
	}
	return nil
}

// SelfUpdate 是"检查 → 下载 → 就位"的一条龙编排, 供 gox update 与 gx/update
// 共用。已是最新时返回 (nil, false, nil)。
//
// target 是要被更新的可执行文件路径; 传 "" 时取当前进程映像
// (os.Executable(), 即自更新语义)。测试与"更新另一个安装位置"场景传显式路径。
//
// 产物落位: <target>.new → Apply 换名 → 成功后 .new 消失, .bak 保留到
// 下次启动清理。调用方在进程退出前仍跑旧映像 (Windows 尤其如此),
// 需要用户重启才真正切到新版本。
func SelfUpdate(target, manifestURL, currentVersion string, o CheckOptions, progress ProgressFunc) (*Release, bool, error) {
	rel, err := Check(manifestURL, currentVersion, o)
	if err != nil || rel == nil {
		return rel, false, err
	}
	if target == "" {
		target, err = os.Executable()
		if err != nil {
			return rel, false, fmt.Errorf("update: 定位当前可执行文件失败: %w", err)
		}
	}
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	newPath := target + ".new"
	if err := Download(rel, newPath, DownloadOptions{Progress: progress}); err != nil {
		return rel, false, err
	}
	if err := Apply(target, newPath); err != nil {
		return rel, false, err
	}
	return rel, true, nil
}

// ===== 内部工具 =====

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func fileSHA256(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

func platformKeys(m map[string]FileEntry) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// semver 是简化版语义化版本: major.minor.patch[-prerelease]。
// prerelease 段按 '.' 切分逐段比较 (纯数字段比大小, 否则按字典序),
// 与 semver 规范的优先级规则同构, 但不做完整校验 —— 更新场景里宽容些没坏处。
type semver struct {
	nums  [3]int
	pre   []string
	isPre bool
}

func parseSemver(s string) (semver, bool) {
	var v semver
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "v"))
	if s == "" {
		return v, false
	}
	core, pre, found := strings.Cut(s, "-")
	if found {
		v.isPre = true
		v.pre = strings.Split(pre, ".")
	}
	parts := strings.SplitN(core, ".", 3)
	if len(parts) == 0 {
		return v, false
	}
	for i := 0; i < 3; i++ {
		if i >= len(parts) {
			v.nums[i] = 0 // "1.2" → 1.2.0
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(parts[i]))
		if err != nil || n < 0 {
			return v, false
		}
		v.nums[i] = n
	}
	return v, true
}

func (a semver) gt(b semver) bool {
	for i := 0; i < 3; i++ {
		if a.nums[i] != b.nums[i] {
			return a.nums[i] > b.nums[i]
		}
	}
	// 数字部分相等: 正式版 > 预发布版
	if a.isPre != b.isPre {
		return b.isPre // a 是正式版 → a 更大
	}
	if !a.isPre {
		return false
	}
	// 都带 pre: 逐段比 (semver 规则: 有段 > 没段; 数字段比数值; 文本段比字典序)
	n := max(len(a.pre), len(b.pre))
	for i := 0; i < n; i++ {
		if i >= len(a.pre) {
			return false // b 段更多 → b 更大
		}
		if i >= len(b.pre) {
			return true // a 段更多 → a 更大
		}
		x, xNum := strconv.Atoi(a.pre[i])
		y, yNum := strconv.Atoi(b.pre[i])
		switch {
		case xNum == nil && yNum == nil:
			if x != y {
				return x > y
			}
		case (xNum == nil) != (yNum == nil):
			return xNum == nil // 数字段 > 文本段
		default:
			if a.pre[i] != b.pre[i] {
				return a.pre[i] > b.pre[i]
			}
		}
	}
	return false
}

func (a semver) eq(b semver) bool {
	for i := 0; i < 3; i++ {
		if a.nums[i] != b.nums[i] {
			return false
		}
	}
	return a.isPre == b.isPre
}
