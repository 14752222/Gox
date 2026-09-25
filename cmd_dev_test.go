package main

import "testing"

func TestIsTempFile(t *testing.T) {
	temp := []string{
		"4913",        // vim 可写性探测文件
		"main.js.swp", // vim swap
		"main.js.swo", // vim swap (旧版)
		"main.js~",    // 备份文件
		".#main.js",   // emacs 锁文件
		"lib.js.tmp",  // 原子写临时文件
		"lib.js.orig", // patch 冲突残留
		"lib.js.rej",  // patch 冲突残留
	}
	for _, name := range temp {
		if !isTempFile(name) {
			t.Errorf("isTempFile(%q) = false, want true", name)
		}
	}
	keep := []string{"main.js", "lib.js", "store.js", "app.js"}
	for _, name := range keep {
		if isTempFile(name) {
			t.Errorf("isTempFile(%q) = true, want false", name)
		}
	}
}

func TestDevInteresting(t *testing.T) {
	if devInteresting("src/lib.js") != true {
		t.Error("普通 .js 应触发重载")
	}
	if devInteresting("src/lib.js.swp") != false {
		t.Error("swap 文件不应触发重载")
	}
	if devInteresting("src/style.css") != false {
		t.Error("非 .js 不应触发重载")
	}
	if devInteresting("src/data.json") != false {
		t.Error("json 不应触发重载")
	}
}
