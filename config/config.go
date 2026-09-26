// Package config 定义并解析 Gox 工程的项目级配置文件 gox.json。
//
// gox.json 是"单一事实来源"：应用名、appId、版本、图标路径、权限声明、
// 各平台子配置都从这里读。刻意只做**纯数据** —— 不允许出现任何脚本/命令
// 字段，避免配置文件变成任意命令执行入口（见计划的"风险提示"一节）。
//
// 示例:
//
//	{
//	  "name": "my-app",
//	  "title": "我的应用",
//	  "appId": "com.example.myapp",
//	  "version": "1.0.0",
//	  "icon": "assets/icon.png",
//	  "permissions": [
//	    "camera",
//	    { "name": "location", "desc": "用于查找附近门店" }
//	  ],
//	  "android": { "minSdk": 24, "targetSdk": 35 },
//	  "ios": { "deploymentTarget": "15.0" },
//	  "desktop": { "windowed": true }
//	}
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// FileName 是项目级配置文件的固定文件名。
const FileName = "gox.json"

// 各字段的缺省值。Default/Load 都以这里为准，避免"两处默认值不一致"。
const (
	DefaultVersion         = "1.0.0"
	DefaultIcon            = "assets/icon.png"
	DefaultMinSDK          = 24
	DefaultTargetSDK       = 35
	DefaultDeploymentTgt   = "15.0"
	DefaultAdaptiveBGColor = "#3DDC84" // Android 自适应图标背景色兜底（Android 绿）
)

// Config 是 gox.json 的内存表示。零值不直接可用 —— 一律通过 Default/Load
// 构造，它们会把缺省值补齐。
type Config struct {
	// Name 是项目名（npm 合法名风格: 小写字母/数字/短横线）。必填。
	Name string `json:"name"`
	// Title 是展示用标题（窗口标题、应用显示名）。缺省取 Name。
	Title string `json:"title"`
	// AppID 是应用唯一标识（Android applicationId / iOS BundleID）。
	// 缺省 com.gox.<Name>（短横线收敛为下划线）。
	AppID string `json:"appId"`
	// Version 是语义化版本号。缺省 1.0.0。
	Version string `json:"version"`
	// Icon 是 1024×1024 源图标路径（相对项目根）。缺省 assets/icon.png。
	Icon string `json:"icon"`
	// Permissions 是声明的权限清单。**未声明的权限一律不写入清单文件**。
	Permissions Permissions `json:"permissions,omitempty"`
	// Android 是 Android 平台子配置。
	Android AndroidConfig `json:"android,omitempty"`
	// IOS 是 iOS 平台子配置。
	IOS IOSConfig `json:"ios,omitempty"`
	// Desktop 是桌面平台子配置。
	Desktop DesktopConfig `json:"desktop,omitempty"`
	// Cert 是签名证书配置。整段缺省 = 使用 certs/ 下 gox cert 生成的
	// 证书（gox build android 会自动生成调试证书）; 用户自备证书时在这
	// 里写路径与密码（密码建议走环境变量注入, 避免入库）。
	Cert CertConfig `json:"cert,omitempty"`
}

// CertConfig 是证书配置段。v1 只接 android（build 链路里唯一自动签名的
// 平台）; windows/harmony/ios 的产物由用户手工喂给 signtool/hap-sign-tool/xcode。
type CertConfig struct {
	// Android 缺省为 nil: 优先读 certs/android-cert.json（gox cert 的元数据）,
	// 连它都没有就自动生成调试证书。自备 keystore 时填这里。
	Android *AndroidSigning `json:"android,omitempty"`
}

// AndroidSigning 描述一个自备的 Android 签名 keystore。
type AndroidSigning struct {
	// Keystore 是 keystore 文件路径（相对项目根或绝对路径）。支持 JKS/PKCS12。
	Keystore string `json:"keystore,omitempty"`
	// Alias 是 key 别名。
	Alias string `json:"alias,omitempty"`
	// StorePassword / KeyPassword 是 keystore 与 key 密码（PKCS12 下两者一致）。
	StorePassword string `json:"storePassword,omitempty"`
	KeyPassword   string `json:"keyPassword,omitempty"`
}

// AndroidConfig 控制 Android 侧的生成与注入。
type AndroidConfig struct {
	// MinSDK / TargetSDK 写入 build.gradle.kts 注入时使用。
	MinSDK    int `json:"minSdk,omitempty"`
	TargetSDK int `json:"targetSdk,omitempty"`
	// AdaptiveBackground 是自适应图标的背景色（#RRGGBB）或背景图层路径。
	// 单图源自动裁剪兜底时，背景用这个纯色。缺省 Android 绿。
	AdaptiveBackground string `json:"adaptiveBackground,omitempty"`
}

// IOSConfig 控制 iOS 侧的生成与注入。
type IOSConfig struct {
	// DeploymentTarget 是最低 iOS 版本（如 "15.0"）。
	DeploymentTarget string `json:"deploymentTarget,omitempty"`
}

// DesktopConfig 控制桌面打包行为。
type DesktopConfig struct {
	// Windowed: Windows 下不显示控制台窗口（-H windowsgui）。缺省 true
	//（GUI 应用不该闪黑框）；显式写 false 才关掉，所以用指针区分"没写"与"写 false"。
	Windowed *bool `json:"windowed,omitempty"`
}

// IsWindowed 报告是否以窗口模式打包（未配置时缺省 true）。
func (d DesktopConfig) IsWindowed() bool {
	return d.Windowed == nil || *d.Windowed
}

// Permission 是一条权限声明：逻辑名 + 可选的自定义用途文案。
// Desc 为空时，注入器使用注册表里的默认文案。
type Permission struct {
	Name string
	Desc string
}

// Permissions 是权限清单，JSON 上兼容两种写法:
//
//	"permissions": ["camera", "microphone"]                 // 简写
//	"permissions": {"camera": "用于拍照", "location": {}}   // 对象: 值可以是字符串或 {desc}
//	"permissions": [{"name": "camera", "desc": "..."}]      // 数组对象形式
//
// 保持声明顺序（对象形式按 Go map 随机序不稳，这里按 key 排序保证幂等输出）。
type Permissions struct {
	entries []Permission
}

// List 返回权限清单副本（保证顺序稳定：声明序或 key 字典序）。
func (p Permissions) List() []Permission {
	out := make([]Permission, len(p.entries))
	copy(out, p.entries)
	return out
}

// Len 返回条数。
func (p Permissions) Len() int { return len(p.entries) }

// Has 报告是否声明了某个逻辑权限。
func (p Permissions) Has(name string) bool {
	for _, e := range p.entries {
		if e.Name == name {
			return true
		}
	}
	return false
}

// UnmarshalJSON 见 Permissions 的类型注释。只认上面三种形状，其它一律报错 ——
// 配置写错时宁可 fail fast，也不要静默丢权限。
func (p *Permissions) UnmarshalJSON(data []byte) error {
	*p = Permissions{}
	trim := strings.TrimSpace(string(data))
	if trim == "null" {
		return nil
	}

	// 数组形式: ["camera", {"name":..., "desc":...}]
	var arr []json.RawMessage
	if err := json.Unmarshal(data, &arr); err == nil {
		for _, raw := range arr {
			var s string
			if err := json.Unmarshal(raw, &s); err == nil {
				p.add(strings.TrimSpace(s), "")
				continue
			}
			var obj struct {
				Name string `json:"name"`
				Desc string `json:"desc"`
			}
			if err := json.Unmarshal(raw, &obj); err != nil {
				return fmt.Errorf("permissions 数组元素必须是字符串或 {name,desc} 对象: %s", trim)
			}
			p.add(strings.TrimSpace(obj.Name), strings.TrimSpace(obj.Desc))
		}
		return nil
	}

	// 对象形式: {"camera": "文案" | {"desc": "文案"}}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("permissions 必须是数组或对象: %s", trim)
	}
	names := make([]string, 0, len(obj))
	for k := range obj {
		names = append(names, k)
	}
	sortStrings(names)
	for _, name := range names {
		raw := obj[name]
		var desc string
		if err := json.Unmarshal(raw, &desc); err == nil {
			p.add(strings.TrimSpace(name), strings.TrimSpace(desc))
			continue
		}
		var sub struct {
			Desc string `json:"desc"`
		}
		if err := json.Unmarshal(raw, &sub); err != nil {
			return fmt.Errorf("permissions.%s 的值必须是字符串或 {\"desc\": ...}", name)
		}
		p.add(strings.TrimSpace(name), strings.TrimSpace(sub.Desc))
	}
	return nil
}

// MarshalJSON 输出数组形式（最简单、可读），保证 round-trip 稳定。
func (p Permissions) MarshalJSON() ([]byte, error) {
	out := make([]map[string]string, 0, len(p.entries))
	for _, e := range p.entries {
		if e.Desc == "" {
			out = append(out, map[string]string{"name": e.Name})
			continue
		}
		out = append(out, map[string]string{"name": e.Name, "desc": e.Desc})
	}
	return json.Marshal(out)
}

func (p *Permissions) add(name, desc string) {
	if name == "" {
		return
	}
	if p.Has(name) {
		return // 重复声明忽略，保持首次出现的位置
	}
	p.entries = append(p.entries, Permission{Name: name, Desc: desc})
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// Default 返回补齐了全部缺省值的配置。
func Default(name string) Config {
	cfg := Config{Name: name}
	cfg.fillDefaults()
	return cfg
}

// Load 从 dir/gox.json 读取配置并补齐缺省值。
// 文件不存在时报错（调用方决定是提示先 create 还是直接失败）。
func Load(dir string) (Config, error) {
	path := filepath.Join(dir, FileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, fmt.Errorf("找不到 %s —— 请在项目根目录运行，或先执行 gox create", path)
		}
		return Config{}, fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	cfg.fillDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s 校验失败: %w", path, err)
	}
	return cfg, nil
}

// Save 把配置写为 dir/gox.json（缩进两格，便于 diff）。
func (c Config) Save(dir string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(dir, FileName), data, 0o644)
}

func (c *Config) fillDefaults() {
	c.Name = strings.TrimSpace(c.Name)
	if c.Title == "" {
		c.Title = c.Name
	}
	if c.Version == "" {
		c.Version = DefaultVersion
	}
	if c.Icon == "" {
		c.Icon = DefaultIcon
	}
	if c.AppID == "" {
		c.AppID = DefaultAppID(c.Name)
	}
	if c.Android.MinSDK == 0 {
		c.Android.MinSDK = DefaultMinSDK
	}
	if c.Android.TargetSDK == 0 {
		c.Android.TargetSDK = DefaultTargetSDK
	}
	if c.Android.AdaptiveBackground == "" {
		c.Android.AdaptiveBackground = DefaultAdaptiveBGColor
	}
	if c.IOS.DeploymentTarget == "" {
		c.IOS.DeploymentTarget = DefaultDeploymentTgt
	}
}

// DefaultAppID 由项目名推导缺省 appId: com.gox.<name>，
// 短横线等非法字符收敛为下划线（Android applicationId 要求合法 Java 包名段）。
func DefaultAppID(name string) string {
	var b strings.Builder
	lastUnderscore := true // 抑制开头的下划线
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore && b.Len() > 0 {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	seg := strings.Trim(b.String(), "_")
	if seg == "" {
		seg = "app"
	}
	return "com.gox." + seg
}

var (
	appIDRe   = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*(\.[a-zA-Z][a-zA-Z0-9_]*)+$`)
	versionRe = regexp.MustCompile(`^\d+\.\d+\.\d+(-[\w.]+)?$`)
)

// Validate 校验关键字段。只拦"必然构建失败"的问题（空的 name、非法的
// appId、不合法的版本号），不做过度限制。
func (c Config) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("name 不能为空")
	}
	if !appIDRe.MatchString(c.AppID) {
		return fmt.Errorf("appId %q 不合法: 需要至少两段、每段以字母开头的反向域名形式（如 com.example.myapp）", c.AppID)
	}
	if !versionRe.MatchString(c.Version) {
		return fmt.Errorf("version %q 不合法: 需要 x.y.z 形式（可带 -后缀）", c.Version)
	}
	if c.Icon == "" {
		return fmt.Errorf("icon 不能为空")
	}
	return nil
}
