package main

import (
	"strings"
	"testing"
)

// TestTestdata 是本工具的负向/正向自测：**一个从没失败过的守卫等于黑盒**。
//
// initpurity 自己也是代码 —— 它可能因为包名改名、正则失配、调用链分析写错而
// 永远输出 "ok"，那时它不但没有防线，还会让人误以为有防线。所以每个判据都要
// 有一个「必须红」的样本，以及一个「必须绿」的对照样本（后者防的是另一种退化：
// 判据写得过宽，把所有 init 都判红，于是没人再信它）。
//
// 样本在 testdata/ 下（Go 工具链不编译该目录，所以里面可以 import windows-only
// 的包而不影响 Linux 上的 go build / go vet）。
func TestTestdata(t *testing.T) {
	cases := []struct {
		dir      string
		wantRed  bool
		wantIn   []string // 输出里必须出现的片段
		wantCall string   // 期望命中的调用文本（空=不校验）
	}{
		{
			dir:     "testdata/bad_direct",
			wantRed: true,
			wantIn:  []string{"平台 IO 包 syscall", "syscall.Getpid()"},
		},
		{
			// 调用链形态：init → setupDevice → syscall.Getpid()
			dir:      "testdata/bad_chain",
			wantRed:  true,
			wantIn:   []string{"init → setupDevice → syscall.Getpid()"},
			wantCall: "syscall.Getpid()",
		},
		{
			dir:     "testdata/bad_platform",
			wantRed: true,
			wantIn:  []string{"平台后端包", "gfx/win32", "win32.EnumAdapters()"},
		},
		{
			// 豁免缺理由 = 不放行
			dir:     "testdata/bad_exempt_noreason",
			wantRed: true,
			wantIn:  []string{"豁免注释缺理由"},
		},
		{
			// 注册表式 init + 闭包里的平台调用：必须放行
			dir:     "testdata/good_registry",
			wantRed: false,
		},
		{
			// 带理由的显式豁免：必须放行
			dir:     "testdata/good_exempt",
			wantRed: false,
		},
	}

	for _, c := range cases {
		got, err := checkDir(c.dir)
		if err != nil {
			t.Fatalf("%s: checkDir 出错：%v", c.dir, err)
		}
		if c.wantRed && len(got) == 0 {
			t.Errorf("%s: 期望判红，实际通过 —— 判据失效了", c.dir)
			continue
		}
		if !c.wantRed && len(got) > 0 {
			for _, v := range got {
				t.Errorf("%s: 期望通过，实际判红：%s:%d %s（%s）",
					c.dir, v.file, v.line, v.call, v.why)
			}
			continue
		}
		if !c.wantRed {
			continue
		}
		// 违规输出里必须带上判据与调用链 —— CI 上只给一句「有违规」是没法归因的
		blob := render(got)
		for _, want := range c.wantIn {
			if !strings.Contains(blob, want) {
				t.Errorf("%s: 输出里缺少 %q\n实际输出：\n%s", c.dir, want, blob)
			}
		}
		if c.wantCall != "" {
			found := false
			for _, v := range got {
				if v.call == c.wantCall {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: 没有命中 %q，实际命中：%s", c.dir, c.wantCall, blob)
			}
		}
	}
}

// render 与 main() 的输出格式保持一致（测试断言的是「人看到的那份文本」）。
func render(all []violation) string {
	var b strings.Builder
	for _, v := range all {
		b.WriteString(v.file + "\n")
		b.WriteString("调用链：" + v.path + "\n")
		b.WriteString("命中：" + v.call + "\n")
		b.WriteString("判据：" + v.why + "\n")
	}
	return b.String()
}

// TestWholeRepoClean 断言**真实源码**当前是干净的：这条不是为了证明工具好用，
// 而是把「仓库现状」钉住 —— 哪天有人在 init 里加了平台调用，这条会红在 CI 上，
// 而不是红在用户的 Windows 机器上。
func TestWholeRepoClean(t *testing.T) {
	dirs, err := expandRoots([]string{"./..."})
	if err != nil {
		t.Fatalf("expandRoots 出错：%v", err)
	}
	if len(dirs) == 0 {
		t.Fatal("expandRoots 没扫到任何包 —— 目录布局变了？")
	}
	var all []violation
	for _, d := range dirs {
		v, err := checkDir(d)
		if err != nil {
			t.Fatalf("%s: %v", d, err)
		}
		all = append(all, v...)
	}
	if len(all) > 0 {
		t.Errorf("真实源码里出现 %d 处 init() 平台调用：\n%s", len(all), render(all))
	}
}

// TestSkipDirs 断言默认扫描不会把 testdata（故意违规的样本）算进来 ——
// 否则 TestWholeRepoClean 永远红，闸门会被人以「它就是红的」为由忽略掉。
func TestSkipDirs(t *testing.T) {
	dirs, err := expandRoots([]string{"./..."})
	if err != nil {
		t.Fatalf("expandRoots 出错：%v", err)
	}
	for _, d := range dirs {
		if strings.Contains(d, "testdata") {
			t.Errorf("默认扫描扫进了 testdata：%s", d)
		}
	}
}
