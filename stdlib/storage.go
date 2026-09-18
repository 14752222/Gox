package stdlib

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

// gx/storage: 应用级 kv 持久化 (设备能力 API 方案 A, 2026-09-19 拍板落地)。
//
//	import { setStorage, getStorage, removeStorage, clearStorage, getStorageInfo,
//	         setAppName, appDataDir } from "gx/storage";
//
//	setStorage("theme", "dark");
//	setStorage("profile", { name: "gox", level: 3 });   // 值经 JSON 序列化
//	getStorage("theme");                                 // "dark"; 不存在返回 undefined (不抛)
//	getStorageInfo();                                    // { keys: [...], currentSize: 1234, limit: -1 }
//
// 语义 (与 uniapp 词汇对齐, 将来走 uni.* 兼容层零重命名):
//   - **全同步 + 写穿透**: 每次 set/remove/clear 立即落盘 (本地文件读写快,
//     C2 论证与剪贴板同构), 不做批量缓冲。
//   - **单文件 JSON 信物**: <appDataDir>/storage.json 整读整写; 写时临时文件
//     + rename 原子替换 (进程中断不会留下半个文件)。
//   - **应用目录约定** (兼容承诺, 拍板 2026-09-18): os.UserConfigDir()/Gox/<appName>
//     (Windows %APPDATA%\Gox\<app>, Linux ~/.config/Gox/<app>, macOS
//     ~/Library/Application Support/Gox/<app>)。appName 优先 setAppName() 显式
//     指定 (推荐), 缺省从 os.Args[1] (脚本路径) 的文件名推, 再落回 "app"。
//   - **limit 恒 -1**: v1 不做配额; currentSize 如实报告 (序列化后的字节数)。
//   - **GOX_STORAGE_DIR 环境变量**整体替换根目录 (测试隔离 / 便携式部署用)。
//
// 刻意的容错: 存储文件损坏按"空存储"处理并告警, 不抛错 —— 数据文件坏了
// 不该让应用起不来; 但写入失败 (磁盘满/目录只读) 会抛错, 静默丢持久化数据
// 比崩溃更难排查。

// setupStorage 注册 gx/storage 模块 (由 Setup 调用, env 参数保持签名一致)。
func setupStorage(env *runtime.Environment) {
	object.RegisterBuiltinModule("gx/storage", func() map[string]object.Value {
		return map[string]object.Value{
			"setAppName":     object.NewBuiltin("setAppName", jsSetAppName),
			"appDataDir":     object.NewBuiltin("appDataDir", jsAppDataDir),
			"setStorage":     object.NewBuiltin("setStorage", jsSetStorage),
			"getStorage":     object.NewBuiltin("getStorage", jsGetStorage),
			"removeStorage":  object.NewBuiltin("removeStorage", jsRemoveStorage),
			"clearStorage":   object.NewBuiltin("clearStorage", jsClearStorage),
			"getStorageInfo": object.NewBuiltin("getStorageInfo", jsGetStorageInfo),
		}
	})
}

var (
	storageMu      sync.Mutex
	storageAppName string // "" = 未定 (首次访问时从 os.Args 推)
	storageData    map[string]object.Value
	storageLoaded  bool
	storageSize    int // 最近一次落盘/加载的字节数
)

// storageResolveDir 计算本次访问的数据目录。
//
// 注意每次都读环境变量而不是 init 时固化: 测试用 t.Setenv 之后才生效的
// 替换也要能被看见。
func storageResolveDir() (string, error) {
	if dir := os.Getenv("GOX_STORAGE_DIR"); dir != "" {
		return filepath.Join(dir, storageCurrentAppName()), nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("gx/storage: 无法定位用户配置目录: %w", err)
	}
	return filepath.Join(base, "Gox", storageCurrentAppName()), nil
}

// storageCurrentAppName 返回当前应用名 (未显式设置时从进程参数推一次并缓存)。
// 调用方须持锁。
func storageCurrentAppName() string {
	if storageAppName != "" {
		return storageAppName
	}
	name := "app"
	if len(os.Args) > 1 {
		base := strings.TrimSuffix(filepath.Base(os.Args[1]), filepath.Ext(os.Args[1]))
		if s := sanitizeAppName(base); s != "" {
			name = s
		}
	}
	storageAppName = name
	return name
}

// sanitizeAppName 把任意字符串收敛成安全的目录名段 ([A-Za-z0-9_-])。
func sanitizeAppName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// storageEnsureLoaded 惰性加载 (首次访问时)。调用方须持锁。
func storageEnsureLoaded() error {
	if storageLoaded {
		return nil
	}
	storageLoaded = true
	storageData = map[string]object.Value{}
	dir, err := storageResolveDir()
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "storage.json"))
	if err != nil {
		return nil // 首次运行: 没有文件 = 空存储, 不是错误
	}
	storageSize = len(raw)
	ordered, err := parseOrderedJSON(string(raw))
	if err != nil {
		// 损坏按空存储处理: 打一行告警, 不让应用起不来
		fmt.Fprintf(os.Stderr, "gx/storage: storage.json 损坏, 已按空存储处理: %v\n", err)
		return nil
	}
	if ordered == nil || ordered.kind != kindObject {
		fmt.Fprintf(os.Stderr, "gx/storage: storage.json 顶层不是对象, 已按空存储处理\n")
		return nil
	}
	for _, k := range ordered.obj.keys {
		storageData[k] = orderedToValue(ordered.obj.values[k])
	}
	return nil
}

// storageFlush 序列化并原子落盘。调用方须持锁。
func storageFlush() error {
	dir, err := storageResolveDir()
	if err != nil {
		return err
	}
	// 键排序 → 输出确定 (diff 友好, 也避免 map 迭代序写进文件)
	keys := make([]string, 0, len(storageData))
	for k := range storageData {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			sb.WriteString(",")
		}
		kb, _ := json.Marshal(k)
		sb.Write(kb)
		sb.WriteString(":")
		sb.WriteString(storageSerialize(storageData[k]))
	}
	sb.WriteString("}")
	data := []byte(sb.String())

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("gx/storage: 创建数据目录失败: %w", err)
	}
	path := filepath.Join(dir, "storage.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("gx/storage: 写入失败: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("gx/storage: 原子替换失败: %w", err)
	}
	storageSize = len(data)
	return nil
}

// storageSerialize 把值序列化成 JSON 文本。与 JSON.stringify 的差异:
// 遇到函数/undefined/Symbol 等 JSON 表达不了的值返回 "null" 并由调用方
// (setStorage) 在前置校验里拦下 —— 持久化存进一个"什么都读不回来"的 null
// 是静默数据丢失, 宁可在写入时抛 TypeError。
func storageSerialize(v object.Value) string {
	switch x := v.(type) {
	case *object.Null, *object.Undefined:
		return "null"
	case *object.Boolean:
		if x.Value {
			return "true"
		}
		return "false"
	case *object.Number:
		return jsValueToJSON(x)
	case *object.String:
		return jsValueToJSON(x)
	case *object.Array:
		parts := make([]string, len(x.Elements))
		for i, e := range x.Elements {
			parts[i] = storageSerialize(e)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case *object.Object:
		keys := make([]string, 0, len(x.Properties))
		for k := range x.Properties {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			kb, _ := json.Marshal(k)
			parts = append(parts, string(kb)+":"+storageSerialize(x.Properties[k].Value))
		}
		return "{" + strings.Join(parts, ",") + "}"
	default:
		return "null"
	}
}

// storageStorable 递归校验值是否可序列化 (纯数据: null/bool/number/string/
// array/object)。循环引用在这里被拦下 (否则序列化会无限递归)。
func storageStorable(v object.Value, depth int, seen map[object.Value]bool) error {
	if depth > 64 {
		return errors.New("值嵌套过深 (>64 层)")
	}
	switch x := v.(type) {
	case *object.Null, *object.Undefined, *object.Boolean, *object.Number, *object.String:
		return nil
	case *object.Array:
		if seen[x] {
			return errors.New("值包含循环引用")
		}
		seen[x] = true
		defer delete(seen, x)
		for _, e := range x.Elements {
			if err := storageStorable(e, depth+1, seen); err != nil {
				return err
			}
		}
		return nil
	case *object.Object:
		if seen[x] {
			return errors.New("值包含循环引用")
		}
		seen[x] = true
		defer delete(seen, x)
		for _, d := range x.Properties {
			if err := storageStorable(d.Value, depth+1, seen); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("值类型 %s 无法序列化 (只支持 null/布尔/数字/字符串/数组/对象)", v.Type())
	}
}

// ===== JS 导出 =====

func jsSetAppName(args ...object.Value) object.Value {
	if len(args) == 0 {
		return object.NewTypeError("setAppName: name required")
	}
	s, ok := args[0].(*object.String)
	if !ok {
		return object.NewTypeError("setAppName: name must be a string")
	}
	name := sanitizeAppName(s.Value)
	if name == "" {
		return object.NewTypeError("setAppName: name has no usable characters (want [A-Za-z0-9_-])")
	}
	storageMu.Lock()
	defer storageMu.Unlock()
	if name == storageAppName {
		return object.UndefinedSingleton
	}
	// 换名 = 换目录: 丢弃已加载的缓存, 下次访问从新目录读。
	// 推荐**在任何 storage 调用之前**先 setAppName —— 之后才换名属于
	// 高级用法, 语义是"后续读写指向新应用", 已写入旧目录的数据不动。
	storageAppName = name
	storageData = nil
	storageLoaded = false
	storageSize = 0
	return object.UndefinedSingleton
}

func jsAppDataDir(args ...object.Value) object.Value {
	storageMu.Lock()
	defer storageMu.Unlock()
	dir, err := storageResolveDir()
	if err != nil {
		return object.NewErrorWithName("Error", err.Error())
	}
	return object.NewString(dir)
}

func jsSetStorage(args ...object.Value) object.Value {
	if len(args) < 2 {
		return object.NewTypeError("setStorage: (key, value) required")
	}
	key, ok := args[0].(*object.String)
	if !ok {
		return object.NewTypeError("setStorage: key must be a string")
	}
	if err := storageStorable(args[1], 0, map[object.Value]bool{}); err != nil {
		return object.NewTypeError("setStorage: %s", err)
	}
	storageMu.Lock()
	defer storageMu.Unlock()
	if err := storageEnsureLoaded(); err != nil {
		return object.NewErrorWithName("Error", err.Error())
	}
	storageData[key.Value] = args[1]
	if err := storageFlush(); err != nil {
		return object.NewErrorWithName("Error", err.Error())
	}
	return object.UndefinedSingleton
}

func jsGetStorage(args ...object.Value) object.Value {
	if len(args) == 0 {
		return object.NewTypeError("getStorage: key required")
	}
	key, ok := args[0].(*object.String)
	if !ok {
		return object.NewTypeError("getStorage: key must be a string")
	}
	storageMu.Lock()
	defer storageMu.Unlock()
	if err := storageEnsureLoaded(); err != nil {
		return object.NewErrorWithName("Error", err.Error())
	}
	if v, ok := storageData[key.Value]; ok {
		return v
	}
	return object.UndefinedSingleton // 不存在不抛 (uniapp 同款语义)
}

func jsRemoveStorage(args ...object.Value) object.Value {
	if len(args) == 0 {
		return object.NewTypeError("removeStorage: key required")
	}
	key, ok := args[0].(*object.String)
	if !ok {
		return object.NewTypeError("removeStorage: key must be a string")
	}
	storageMu.Lock()
	defer storageMu.Unlock()
	if err := storageEnsureLoaded(); err != nil {
		return object.NewErrorWithName("Error", err.Error())
	}
	delete(storageData, key.Value)
	if err := storageFlush(); err != nil {
		return object.NewErrorWithName("Error", err.Error())
	}
	return object.UndefinedSingleton
}

func jsClearStorage(args ...object.Value) object.Value {
	storageMu.Lock()
	defer storageMu.Unlock()
	if err := storageEnsureLoaded(); err != nil {
		return object.NewErrorWithName("Error", err.Error())
	}
	storageData = map[string]object.Value{}
	if err := storageFlush(); err != nil {
		return object.NewErrorWithName("Error", err.Error())
	}
	return object.UndefinedSingleton
}

func jsGetStorageInfo(args ...object.Value) object.Value {
	storageMu.Lock()
	defer storageMu.Unlock()
	if err := storageEnsureLoaded(); err != nil {
		return object.NewErrorWithName("Error", err.Error())
	}
	keys := make([]string, 0, len(storageData))
	for k := range storageData {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	elems := make([]object.Value, len(keys))
	for i, k := range keys {
		elems[i] = object.NewString(k)
	}
	info := object.NewObject()
	info.SetProperty("keys", object.NewArray(elems))
	info.SetProperty("currentSize", object.NewNumber(float64(storageSize)))
	info.SetProperty("limit", object.NewNumber(-1)) // v1 不做配额
	return info
}
