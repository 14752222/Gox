package gfx

import (
	"testing"
)

// ===== 媒体句柄的全局唯一性 =====
//
// 这三个用例钉住的是同一条不变量: ResolveVideoEvent / ResolveAudioEvent 在
// **全局**的 videoStates / audioStates 里按句柄回表, 句柄因此必须进程内唯一。
// 后端自己从 1 编号的话, 两个窗口各自的第一个 <video> 都会拿到 1, 事件就会串
// 到别的窗口的节点上 (实测: A 窗口的视频事件打进 B 窗口的脚本, 在 B 的
// globalThis 还没赋值时触发回调 → "Cannot read properties of undefined")。
// 而且它时好时坏 —— 撞车后命中哪一个取决于 map 迭代序。

// TestVideoHandleIsGloballyUnique 两个窗口各自的第一个 video 不能拿到同一个句柄。
func TestVideoHandleIsGloballyUnique(t *testing.T) {
	startVideoTest(t)

	mk := func() (uint64, *GuiNode) {
		root := mkNode("column", nil)
		v := mkVideoNode("movie.mp4", map[string]float64{"width": 80, "height": 45})
		mountChildren(root, v)
		_, _, vh := mountVideoApp(t, root, 400, 300)
		return vh.lastID(), v
	}

	idA, nodeA := mk()
	idB, nodeB := mk()
	if idA == 0 || idB == 0 {
		t.Fatalf("两个窗口都该挂上表面, 实际 idA=%d idB=%d", idA, idB)
	}
	if idA == idB {
		t.Fatalf("两个窗口的第一个 <video> 拿到了同一个句柄 %d —— 事件回表会串窗口", idA)
	}
	if got := videoSurfaceID(nodeA); got != idA {
		t.Fatalf("窗口 A 的节点句柄 = %d, want %d", got, idA)
	}
	if got := videoSurfaceID(nodeB); got != idB {
		t.Fatalf("窗口 B 的节点句柄 = %d, want %d", got, idB)
	}
}

// TestAudioHandleIsGloballyUnique 同上, 音频侧。
func TestAudioHandleIsGloballyUnique(t *testing.T) {
	startAudioTest(t)

	mk := func() uint64 {
		root := mkNode("column", nil)
		n := mkAudioNode("song.wav", nil)
		mountChildren(root, n)
		_, _, ah := mountAudioApp(t, root, 400, 300)
		return ah.lastID()
	}

	idA, idB := mk(), mk()
	if idA == 0 || idB == 0 {
		t.Fatalf("两个窗口都该挂上音频源, 实际 idA=%d idB=%d", idA, idB)
	}
	if idA == idB {
		t.Fatalf("两个窗口的第一个 <audio> 拿到了同一个句柄 %d —— 事件回表会串窗口", idA)
	}
}

// TestVideoCloseReleasesHandles 关窗时该窗口的句柄要一次摘干净。
//
// flush 只在有脏区重绘时才跑, 而窗口关闭之后永远等不到那一次 —— 不清的话那些
// 条目会一直留在全局表里, 节点与脚本都已销毁, 只剩"句柄还在、回调已死"的僵尸。
func TestVideoCloseReleasesHandles(t *testing.T) {
	startVideoTest(t)

	root := mkNode("column", nil)
	v := mkVideoNode("movie.mp4", map[string]float64{"width": 80, "height": 45})
	mountChildren(root, v)

	_, a, vh := mountVideoApp(t, root, 400, 300)
	id := vh.lastID()
	if id == 0 {
		t.Fatal("首帧该挂上表面")
	}

	a.close()

	if got := vh.detachCalls(); len(got) != 1 || got[0] != id {
		t.Fatalf("关窗该摘掉本窗口的表面, detach=%v (id=%d)", got, id)
	}
	if len(vh.live) != 0 {
		t.Fatalf("关窗后宿主不该还有挂着的表面: %v", vh.live)
	}
	if _, ok := videoStateOf(v); ok {
		t.Fatal("关窗后状态表该清干净")
	}
}
