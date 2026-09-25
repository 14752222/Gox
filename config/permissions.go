package config

import (
	"fmt"
	"strings"
)

// PermissionSpec 是一条逻辑权限在各平台的落地方式。
//
// Android 的 UsesPermissions 原样写入 <uses-permission android:name="...">;
// 每条 iOS 键配一句默认用途文案（中文兜底 —— 上架审核需要"人话", 默认值
// 只是兜底, 文档提醒用户在 gox.json 里自定义）。
type PermissionSpec struct {
	// Android 的 uses-permission 权限串; 可多条（如 location 同时要
	// FINE + COARSE）。为空表示 Android 侧无需声明（如 notifications）。
	AndroidUses []string
	// IOSKey 是 Info.plist 的用途描述键（NSxxxUsageDescription）。
	// 为空表示 iOS 侧无需声明。
	IOSKey string
	// IOSDefault 是 iOS 用途描述的默认文案。
	IOSDefault string
}

// 权限注册表: 逻辑权限名 → 各平台声明。
//
// 只收这一张表 —— 加新权限就是在这里加一行, 注入器与文档都从表驱动,
// 不存在"清单文件里多写一份"的第二真相来源。
var PermissionRegistry = map[string]PermissionSpec{
	"camera": {
		AndroidUses: []string{"android.permission.CAMERA"},
		IOSKey:      "NSCameraUsageDescription",
		IOSDefault:  "需要使用相机进行拍照和录制视频。",
	},
	"microphone": {
		AndroidUses: []string{"android.permission.RECORD_AUDIO"},
		IOSKey:      "NSMicrophoneUsageDescription",
		IOSDefault:  "需要使用麦克风进行录音。",
	},
	"location": {
		AndroidUses: []string{
			"android.permission.ACCESS_FINE_LOCATION",
			"android.permission.ACCESS_COARSE_LOCATION",
		},
		IOSKey:     "NSLocationWhenInUseUsageDescription",
		IOSDefault: "需要获取您的位置信息以提供位置相关功能。",
	},
	"storage": {
		AndroidUses: []string{
			"android.permission.READ_MEDIA_IMAGES",
			"android.permission.READ_EXTERNAL_STORAGE",
		},
		IOSKey:     "NSPhotoLibraryAddUsageDescription",
		IOSDefault: "需要访问相册以保存或读取图片。",
	},
	"photos": {
		AndroidUses: []string{"android.permission.READ_MEDIA_IMAGES"},
		IOSKey:      "NSPhotoLibraryUsageDescription",
		IOSDefault:  "需要访问相册以选择照片。",
	},
	"notifications": {
		// Android 13+ 运行时权限; iOS 本地通知不需要用途描述键。
		AndroidUses: []string{"android.permission.POST_NOTIFICATIONS"},
	},
	"bluetooth": {
		AndroidUses: []string{
			"android.permission.BLUETOOTH_SCAN",
			"android.permission.BLUETOOTH_CONNECT",
		},
		IOSKey:     "NSBluetoothAlwaysUsageDescription",
		IOSDefault: "需要使用蓝牙连接外部设备。",
	},
	"contacts": {
		AndroidUses: []string{"android.permission.READ_CONTACTS"},
		IOSKey:      "NSContactsUsageDescription",
		IOSDefault:  "需要访问通讯录以选择联系人。",
	},
	"biometrics": {
		AndroidUses: []string{"android.permission.USE_BIOMETRIC"},
		IOSKey:      "NSFaceIDUsageDescription",
		IOSDefault:  "需要使用面容 ID / 触控 ID 进行身份验证。",
	},
}

// LookupPermission 查注册表。未知权限直接报错 —— 静默忽略会让用户以为
// 权限已生效（上线后弹窗不出、功能挂掉), 宁可 fail fast。
func LookupPermission(name string) (PermissionSpec, error) {
	name = strings.TrimSpace(strings.ToLower(name))
	spec, ok := PermissionRegistry[name]
	if !ok {
		return PermissionSpec{}, fmt.Errorf("未知权限 %q —— 可用权限: %s", name, KnownPermissions())
	}
	return spec, nil
}

// KnownPermissions 返回注册表里的权限名（排序稳定, 供报错与文档使用）。
func KnownPermissions() []string {
	names := make([]string, 0, len(PermissionRegistry))
	for name := range PermissionRegistry {
		names = append(names, name)
	}
	sortStrings(names)
	return names
}

// AndroidUses 汇总权限清单的全部 Android uses-permission。
// 任何清单都额外带 INTERNET（最小权限基线, Gox 运行时自身需要）。
func AndroidUses(perms []Permission) ([]string, error) {
	seen := map[string]bool{"android.permission.INTERNET": true}
	out := []string{"android.permission.INTERNET"}
	for _, p := range perms {
		spec, err := LookupPermission(p.Name)
		if err != nil {
			return nil, err
		}
		for _, u := range spec.AndroidUses {
			if !seen[u] {
				seen[u] = true
				out = append(out, u)
			}
		}
	}
	return out, nil
}

// IOSUsage 汇总权限清单的全部 iOS 用途描述键 → 文案。
// 自定义文案（p.Desc 非空）优先, 否则用注册表默认兜底。
func IOSUsage(perms []Permission) (map[string]string, error) {
	out := map[string]string{}
	for _, p := range perms {
		spec, err := LookupPermission(p.Name)
		if err != nil {
			return nil, err
		}
		if spec.IOSKey == "" {
			continue
		}
		if _, dup := out[spec.IOSKey]; dup {
			continue
		}
		if p.Desc != "" {
			out[spec.IOSKey] = p.Desc
		} else {
			out[spec.IOSKey] = spec.IOSDefault
		}
	}
	return out, nil
}
