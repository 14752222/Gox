package permissions

import "testing"

func TestDefaultIsAllForEmbedding(t *testing.T) {
	if Granted() != All {
		t.Fatalf("库默认应为 All（嵌入式场景），得到 %v", Granted())
	}
}

func TestRequireDeniedThenGranted(t *testing.T) {
	Set(0)
	defer Set(All)
	if err := Require(FSWrite); err == nil {
		t.Fatal("最小权限下 FSWrite 应被拒绝")
	}
	Set(FSWrite)
	if err := Require(FSWrite); err != nil {
		t.Fatalf("授权后 FSWrite 应放行，得到 %v", err)
	}
	if err := Require(NetListen); err == nil {
		t.Fatal("只授 FSWrite 时 NetListen 仍应被拒绝")
	}
}

func TestParseFlags(t *testing.T) {
	rest, mask, err := ParseFlags([]string{"--allow-fs-write", "app.js", "--allow-all"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 1 || rest[0] != "app.js" {
		t.Fatalf("标志应被剥离，得到 %v", rest)
	}
	if mask != All {
		t.Fatalf("--allow-all 应授予全部，得到 %v", mask)
	}
}

func TestParseFlagsUnknown(t *testing.T) {
	if _, _, err := ParseFlags([]string{"--allow-fswrite"}); err == nil {
		t.Fatal("打错字的权限标志必须报错（安全相关 flag 不能静默忽略）")
	}
}

func TestDeniedErrorMentionsFlag(t *testing.T) {
	Set(0)
	defer Set(All)
	err := Require(ProcessExit)
	if err == nil || !contains(err.Inspect(), "--allow-process-exit") {
		t.Fatalf("报错应点名需要的标志，得到 %v", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
