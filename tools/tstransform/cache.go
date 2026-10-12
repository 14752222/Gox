package tstransform

// cache.go —— 持久化转译缓存 (M2 P0-2)。
//
// 动机: 每次 `gox dev` 重载、每次冷启动进程、每次 import 同一个 .ts 模块,
// esbuild 都要重跑一遍完整的 parse/transform。对"改一行 → 界面更新 < 1s"的
// 目标来说, 重跑整棵依赖树是主要成本之一。转译产物只依赖 (文件内容 + 转译
// 选项), 天然可缓存 —— 缓存键里带上内容 hash, 内容一变键就变, 不存在脏读。
//
// 落盘位置: `$GOX_CACHE_DIR`(若设) 否则 `~/.gox/cache`, 子目录 `transpile/v<N>`。
//   - 版本号进**路径前缀**: tstransform 的转译选项(loader/JSX/decorators)变了
//     就 bump cacheVersion, 老缓存整目录析构、无需逐条比对;
//   - 内容 hash 进**文件名**: 同路径改内容 = 新文件, 老键自然淘汰(不删也不影响
//     正确性, 交给用户/系统清 ~/.gox)。
//
// 损坏处理: 读不到 / JSON 坏了 / 字段缺失, 一律当未命中, 并尽力删掉坏文件后
// 重新生成 —— 缓存是纯派生数据, 任何异常都不该影响主流程。
//
// 观测: `GOX_TS_DEBUG=1` 时向 stderr 打印 hit/miss, 便于验证"第二次真没重算"。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// cacheVersion 是缓存格式 / 转译选项的版本号。
//
// 改这里等于让全部老缓存失效。以下任一变化都必须 bump:
//   - loader / JSX 模式 / TsconfigRaw(含装饰器开关) 变化;
//   - cacheRecord 的字段语义变化。
const cacheVersion = 1

// cacheRecord 是落盘的一条缓存。Map 存 esbuild 原始 sourcemap JSON, 命中时
// 现场重新解码成 LineMap —— VLQ 解码很便宜, 不值得把解码结果再序列化一遍
// (序列化结构反而更容易随内部实现漂移而失效)。
type cacheRecord struct {
	Version int    `json:"version"`
	Code    string `json:"code"`
	Map     string `json:"map,omitempty"`
}

// TransformCached 是带持久化缓存的 Transform。
//
// path 建议传绝对路径 (缓存键含它; 相对路径在不同 cwd 下会得到不同键, 语义上
// 也确实是不同文件)。转译失败不缓存 —— 错误通常是暂时的 (用户正在改文件)。
func TransformCached(source []byte, path string) (*Result, error) {
	key := cacheKey(source, path)
	if rec, ok := loadCache(key); ok {
		debugCache("hit", path)
		lm, _ := DecodeLineMap([]byte(rec.Map))
		return &Result{Code: []byte(rec.Code), LineMap: lm}, nil
	}
	debugCache("miss", path)

	res, err := Transform(source, path)
	if err != nil {
		return nil, err
	}
	storeCache(key, res)
	return res, nil
}

// cacheKey = sha256(版本 + 绝对路径 + 选项标签 + 源内容)。
// 选项标签固定为当前编译期常量 (loader 由 ext 决定, 已含在 path 里)。
func cacheKey(source []byte, path string) string {
	abs := path
	if a, err := filepath.Abs(path); err == nil {
		abs = a
	}
	h := sha256.New()
	fmt.Fprintf(h, "gox-transpile-v%d\x00", cacheVersion)
	h.Write([]byte(abs))
	h.Write([]byte{0})
	h.Write([]byte(tsconfigRawJSON))
	h.Write([]byte{0})
	h.Write(source)
	return hex.EncodeToString(h.Sum(nil))
}

// cacheBaseDir 返回缓存根目录 (未含版本前缀)。
func cacheBaseDir() string {
	if d := os.Getenv("GOX_CACHE_DIR"); d != "" {
		return filepath.Join(d, "transpile")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		// 拿不到 home 时退回系统临时目录 —— 缓存失效总比整个转译失败好。
		return filepath.Join(os.TempDir(), "gox-cache", "transpile")
	}
	return filepath.Join(home, ".gox", "cache", "transpile")
}

// cacheDir 返回带版本前缀的实际目录。
func cacheDir() string {
	return filepath.Join(cacheBaseDir(), fmt.Sprintf("v%d", cacheVersion))
}

func cacheFilePath(key string) string {
	return filepath.Join(cacheDir(), key+".json")
}

// loadCache 读取并校验一条缓存。任何异常都返回 false (未命中)。
func loadCache(key string) (*cacheRecord, bool) {
	p := cacheFilePath(key)
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	var rec cacheRecord
	if err := json.Unmarshal(data, &rec); err != nil || rec.Version != cacheVersion {
		// 损坏 / 版本不符: 删除坏文件, 当作未命中。删失败也无所谓。
		_ = os.Remove(p)
		return nil, false
	}
	return &rec, true
}

// storeCache 原子写一条缓存 (临时文件 + rename), 失败静默 —— 缓存不可写
// (只读 HOME 等) 不该让转译失败。
func storeCache(key string, res *Result) {
	rec := cacheRecord{Version: cacheVersion, Code: string(res.Code)}
	if res.LineMap != nil {
		// 重新编码为 map JSON: 我们只需要保留原始 mappings, 用轻量结构重建。
		rec.Map = res.LineMap.encodeJSON()
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return
	}
	dir := cacheDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	tmp := filepath.Join(dir, key+".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		_ = os.Remove(tmp)
		return
	}
	if err := os.Rename(tmp, cacheFilePath(key)); err != nil {
		_ = os.Remove(tmp)
	}
}

// cacheDebugEnabled 报告是否打印缓存命中日志。
//
// 刻意不做 sync.Once 缓存: 一是 getenv 的开销远小于一次 esbuild 转译, 二是
// 缓存住会让"测试里刚设的 GOX_TS_DEBUG 不生效"(环境变量在进程生命周期内变化
// 的测试场景), 观测开关不值得用正确性换。
func cacheDebugEnabled() bool {
	return os.Getenv("GOX_TS_DEBUG") != ""
}

func debugCache(kind, path string) {
	if !cacheDebugEnabled() {
		return
	}
	fmt.Fprintf(os.Stderr, "[tstransform] cache %s %s\n", kind, path)
}

// encodeJSON 把行映射重新编码成 source map 兼容的 JSON。
//
// 只需要 sources + mappings 两个字段 (DecodeLineMap 只读它们), 因此把段表
// 重新编码回 VLQ 而不是保存内部结构。这样缓存的跨版本兼容性只看 cacheVersion,
// 不依赖 LineMap 的内存布局。
func (m *LineMap) encodeJSON() string {
	if m == nil {
		return ""
	}
	var b []byte
	prevSrcLine, prevSrcCol := 0, 0
	for li, segs := range m.lines {
		if li > 0 {
			b = append(b, ';')
		}
		prevGenCol := 0
		first := true
		for _, s := range segs {
			if s.srcLine == 0 {
				continue // 合成代码段不写回, 解码端也不会需要它
			}
			if !first {
				b = append(b, ',')
			}
			first = false
			// 单源 (sources[0]), srcIdx 恒为 0。
			b = appendVLQ(b, s.genCol-prevGenCol)
			b = appendVLQ(b, 0) // srcIdx delta
			b = appendVLQ(b, (s.srcLine-1)-prevSrcLine)
			b = appendVLQ(b, (s.srcCol-1)-prevSrcCol)
			prevGenCol = s.genCol
			prevSrcLine = s.srcLine - 1
			prevSrcCol = s.srcCol - 1
		}
	}
	enc, _ := json.Marshal(map[string]any{
		"sources":  m.sourceNames,
		"mappings": string(b),
	})
	return string(enc)
}

// appendVLQ 把一个有符号整数按 source map v3 的 base64 VLQ 编码追加到 b。
func appendVLQ(b []byte, v int) []byte {
	var u uint32
	if v < 0 {
		u = uint32(-v)<<1 | 1
	} else {
		u = uint32(v) << 1
	}
	for {
		digit := u & 31
		u >>= 5
		if u != 0 {
			digit |= 32
		}
		b = append(b, base64VLQ[digit])
		if u == 0 {
			return b
		}
	}
}
