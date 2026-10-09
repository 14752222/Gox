package main

import (
	"strings"
	"testing"

	"github.com/14752222/Gox/gfx"
)

// TestInitDevMode 钉住 GOX_DEV 的取值口径。
//
// 为什么值得单测: 这是个"只在开发环境生效"的开关, 判反了不会让任何测试变红
// (生产默认关, 而 CI 也不设这个环境变量) —— 它是那种"坏了半年没人发现"的
// 配置项。特别是 GOX_DEV=0 必须等于关: 若只看"设没设", CI 里想显式关掉的人
// 会把诊断全打开。
func TestInitDevMode(t *testing.T) {
	orig := gfx.DevMode()
	t.Cleanup(func() { gfx.SetDevMode(orig) })

	for _, c := range []struct {
		env  string
		want bool
	}{
		{"1", true},
		{"true", true},
		{"TRUE", true}, // 大小写不敏感: 手敲环境变量没人记得住大小写
		{"yes", true},
		{"on", true},
		{" 1 ", true}, // 前后空格: shell 里 export GOX_DEV=1 很容易带进来
		{"0", false},
		{"false", false},
		{"", false},
		{"   ", false},
		{"maybe", false}, // 不认识的值一律关, 不猜
	} {
		t.Setenv("GOX_DEV", c.env)
		gfx.SetDevMode(false)
		initDevMode()
		if got := gfx.DevMode(); got != c.want {
			t.Errorf("GOX_DEV=%q ⇒ dev 模式 = %v, want %v", c.env, got, c.want)
		}
	}
}

// TestDevSpawnInjectsDevMode 钉住子进程继承开发模式。
//
// 为什么值得单测: 进程级热重启跑的是"同一个入口的另一个 gox 进程", 它自己
// 读不到"我是被 dev 拉起来的"。环境变量是唯一的传递通道 —— 漏了这一行,
// 重启之后窗口照常出来, 但全部静默失败诊断都会消失, 而那正是 dev 模式存在
// 的理由。这种"看起来完全正常"的失效, 只有断言环境变量能抓住。
func TestDevSpawnInjectsDevMode(t *testing.T) {
	cmd, err := devSpawn("entry.js")
	if err != nil {
		t.Fatalf("devSpawn: %v", err)
	}
	for _, e := range cmd.Env {
		if e == "GOX_DEV=1" {
			return
		}
	}
	t.Fatalf("子进程环境里没有 GOX_DEV=1 (共 %d 项, 含 GOX_DEV 的: %v)",
		len(cmd.Env), devEnvEntries(cmd.Env))
}

// devEnvEntries 取出环境里所有 GOX_DEV 相关项, 供失败信息打印。
func devEnvEntries(env []string) []string {
	var out []string
	for _, e := range env {
		if strings.HasPrefix(e, "GOX_DEV") {
			out = append(out, e)
		}
	}
	return out
}
