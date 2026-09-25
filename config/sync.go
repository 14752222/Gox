package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 注入标记。sync 幂等的关键: 区块整体替换 —— 找到 START..END 之间的全部内容
// 一并换掉, 重复执行永远只产出同一份文件, 不会重复追加。
const (
	androidStart = "<!--GOX:PERMISSIONS:START-->"
	androidEnd   = "<!--GOX:PERMISSIONS:END-->"
	iosStart     = "<!--GOX:USAGE:START-->"
	iosEnd       = "<!--GOX:USAGE:END-->"
)

// Sync 把 cfg.Permissions 幂等注入项目里的两份清单文件:
//
//	android/AndroidManifest.xml  <!--GOX:PERMISSIONS--> 区块
//	ios/Info.plist               <!--GOX:USAGE--> 区块
//
// 返回实际被修改的文件（斜杠相对路径）。文件内容没变化时不落盘,
// 所以 `gox sync` 跑多少次都不会弄脏构建。
func Sync(dir string, cfg Config) ([]string, error) {
	perms := cfg.Permissions.List()

	androidPath := filepath.Join(dir, "android", "AndroidManifest.xml")
	if err := syncAndroidManifest(androidPath, perms); err != nil {
		return nil, err
	}
	iosPath := filepath.Join(dir, "ios", "Info.plist")
	if err := syncIOSInfoPlist(iosPath, perms); err != nil {
		return nil, err
	}
	return []string{"android/AndroidManifest.xml", "ios/Info.plist"}, nil
}

// syncAndroidManifest 注入 <uses-permission> 声明。
func syncAndroidManifest(path string, perms []Permission) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("找不到 %s —— 请先 gox create 或在项目根运行", path)
		}
		return err
	}
	uses, err := AndroidUses(perms)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	var b strings.Builder
	b.WriteString(androidStart + "\n")
	b.WriteString("    <!-- 由 gox sync 从 gox.json 的 permissions 生成, 区块内勿手工编辑 -->\n")
	for _, u := range uses {
		fmt.Fprintf(&b, "    <uses-permission android:name=%q />\n", u)
	}
	b.WriteString(androidEnd)

	updated, changed, err := replaceMarkedBlock(string(data), androidStart, androidEnd, b.String())
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if changed {
		return writeFileIfChanged(path, []byte(updated))
	}
	return nil
}

// syncIOSInfoPlist 注入 NSxxxUsageDescription 键值对（用途描述）。
func syncIOSInfoPlist(path string, perms []Permission) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("找不到 %s —— 请先 gox create 或在项目根运行", path)
		}
		return err
	}
	usage, err := IOSUsage(perms)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	// key 排序输出, 保证同一份输入永远产出同一份文件（幂等的另一半）
	keys := make([]string, 0, len(usage))
	for k := range usage {
		keys = append(keys, k)
	}
	sortStrings(keys)

	var b strings.Builder
	b.WriteString(iosStart + "\n")
	b.WriteString("\t<!-- 由 gox sync 从 gox.json 的 permissions 生成, 区块内勿手工编辑 -->\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "\t<key>%s</key>\n\t<string>%s</string>\n", k, usage[k])
	}
	b.WriteString(iosEnd)

	updated, changed, err := replaceMarkedBlock(string(data), iosStart, iosEnd, b.String())
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if changed {
		return writeFileIfChanged(path, []byte(updated))
	}
	return nil
}

// replaceMarkedBlock 把 start..end 标记区块整体替换为 newBlock。
// 标记不存在时返回错误 —— 模板自带标记, 缺了说明用户删了区块, 强制
// 显式失败而不是悄悄往文件尾部追加（那会生成坏 XML）。
func replaceMarkedBlock(content, start, end, newBlock string) (string, bool, error) {
	i := strings.Index(content, start)
	j := strings.Index(content, end)
	if i >= 0 != (j >= 0) {
		return "", false, fmt.Errorf("标记区块不完整（只找到一半）, 请恢复 %s ... %s 结构", start, end)
	}
	if i < 0 {
		return "", false, fmt.Errorf("找不到标记区块 %s —— 请从模板恢复该区块", start)
	}
	if j < i {
		return "", false, fmt.Errorf("标记区块顺序颠倒: %s 出现在 %s 之后", start, end)
	}
	blockEnd := j + len(end)
	if strings.TrimSpace(content[i:blockEnd]) == strings.TrimSpace(newBlock) {
		return content, false, nil // 内容一致, 不动文件
	}
	return content[:i] + newBlock + content[blockEnd:], true, nil
}

// writeFileIfChanged 内容与磁盘一致时不写, 保住 mtime（下游增量工具友好）。
func writeFileIfChanged(path string, data []byte) error {
	if old, err := os.ReadFile(path); err == nil && string(old) == string(data) {
		return nil
	}
	return os.WriteFile(path, data, 0o644)
}
